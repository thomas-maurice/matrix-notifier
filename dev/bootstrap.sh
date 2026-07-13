#!/usr/bin/env bash
# Bootstraps the dev stack: Synapse + Postgres, an admin user, the bot
# account, an encrypted room with the bot invited, and config.dev.yaml at the
# repo root. Idempotent: safe to re-run.
set -euo pipefail
cd "$(dirname "$0")"

HS=http://localhost:8008
ADMIN_USER=admin
ADMIN_PASS=admin
BOT_USER=notifier
BOT_PASS=notifier-dev-password

command -v jq >/dev/null || { echo "jq is required (brew install jq)" >&2; exit 1; }

echo "==> Generating Synapse keys (first run only)"
mkdir -p synapse/data
if [ ! -f synapse/data/localhost.signing.key ]; then
  docker compose run --rm synapse generate
fi
cp synapse/homeserver.template.yaml synapse/data/homeserver.yaml

echo "==> Starting Synapse + Postgres"
docker compose up -d --wait

echo "==> Registering admin user (idempotent)"
docker compose exec synapse register_new_matrix_user \
  -c /data/homeserver.yaml -u "$ADMIN_USER" -p "$ADMIN_PASS" -a http://localhost:8008 \
  2>&1 | grep -v "User ID already taken" || true

echo "==> Logging in as admin"
ADMIN_TOKEN=$(curl -sf -X POST "$HS/_matrix/client/v3/login" -d "{
  \"type\": \"m.login.password\",
  \"identifier\": {\"type\": \"m.id.user\", \"user\": \"$ADMIN_USER\"},
  \"password\": \"$ADMIN_PASS\"
}" | jq -r .access_token)
[ -n "$ADMIN_TOKEN" ] && [ "$ADMIN_TOKEN" != "null" ] || { echo "admin login failed" >&2; exit 1; }

echo "==> Creating bot account @$BOT_USER:localhost (idempotent)"
curl -sf -X PUT -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$HS/_synapse/admin/v2/users/@$BOT_USER:localhost" \
  -d "{\"password\": \"$BOT_PASS\", \"admin\": false, \"logout_devices\": false}" >/dev/null

echo "==> Creating notifier database for the crypto store (idempotent)"
docker compose exec postgres psql -U synapse -tAc \
  "SELECT 1 FROM pg_database WHERE datname='notifier'" | grep -q 1 || \
  docker compose exec postgres psql -U synapse -c "CREATE DATABASE notifier"

echo "==> Creating encrypted room and inviting the bot (once)"
if [ -s .room_id ]; then
  ROOM_ID=$(cat .room_id)
  echo "    reusing $ROOM_ID"
else
  ROOM_ID=$(curl -sf -X POST -H "Authorization: Bearer $ADMIN_TOKEN" \
    "$HS/_matrix/client/v3/createRoom" -d "{
      \"name\": \"Notifications\",
      \"preset\": \"private_chat\",
      \"invite\": [\"@$BOT_USER:localhost\"],
      \"initial_state\": [{
        \"type\": \"m.room.encryption\",
        \"state_key\": \"\",
        \"content\": {\"algorithm\": \"m.megolm.v1.aes-sha2\"}
      }]
    }" | jq -r .room_id)
  [ -n "$ROOM_ID" ] && [ "$ROOM_ID" != "null" ] || { echo "room creation failed" >&2; exit 1; }
  echo "$ROOM_ID" > .room_id
  echo "    created $ROOM_ID"
fi

echo "==> Writing config.dev.yaml (admin token: dev-admin-token)"
ADMIN_TOKEN_HASH=$(cd .. && go run -tags goolm ./cmd/matrix-notifier token hash dev-admin-token)
sed "s|@ADMIN_TOKEN_HASH@|$ADMIN_TOKEN_HASH|" config.dev.template.yaml > ../config.dev.yaml

cat <<EOF

Dev stack ready.
  Homeserver:    $HS  (server_name: localhost)
  Element Web:   http://localhost:8009
  Synapse admin: http://localhost:8010  (homeserver URL: $HS)
  Admin UI:      http://localhost:8686  (admin token: dev-admin-token)
  Admin user:    @$ADMIN_USER:localhost / $ADMIN_PASS
  Bot user:      @$BOT_USER:localhost / $BOT_PASS
  Room:          $ROOM_ID (encrypted)
  Bot config:    config.dev.yaml

Run the bot:    make run-dev
Seed a channel+token (bot must be running):  make dev-seed
EOF
