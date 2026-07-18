---
name: matrix-notifier-dev
description: How to work on the matrix-notifier repo — layout, dev stack lifecycle (up/seed/down/nuke), building, testing with cmdclient, talking to the bot, and troubleshooting. Use for ANY development, debugging, or ops task in this repository.
---

# Working on matrix-notifier

Go Matrix notification gateway: HTTP ingest endpoints (Gotify, Alertmanager,
Gitea/Forgejo, Slack-compatible) → end-to-end-encrypted Matrix rooms. Routing is **channels**
(name → room ID) with **ingest tokens** belonging to channels; both live in
the DB and are managed via a Connect RPC admin API + embedded Vue UI, never
in the config file.

## Maintenance contract (non-negotiable)

When you add or change **anything** — a feature, a procedure, a Make target,
a config key, a troubleshooting trick — you MUST update in the same session:

1. **`README.md`** — the user-facing documentation.
2. **This skill** — the agent-facing documentation.

Also search cortex (`cortex_memory_search`, namespace is auto-derived from
the git remote) at task start: this repo has a rich decision history
(architecture, E2EE gotchas, prod deployment). Save new decisions/gotchas
back to cortex when done.

**Never commit or push** — Thomas reviews diffs and commits himself.

## Repo structure

```
cmd/matrix-notifier/    main, `send` subcommand (CLI client), `token hash`
internal/
  matrix/     bot: login, E2EE (mautrix cryptohelper), cross-signing, SAS
              verification, key backup, !notify commands, room helpers
  api/        Connect RPC AdminService (channels/tokens/status/rooms) +
              auth.go: JWT session auth (Login/Logout/ChangeAdminPassword)
  server/     gin HTTP server: ingest routes, /health, /metrics, per-token
              rate limiting, serves the embedded UI
  store/      GORM store (channels + ingest tokens + admin credential),
              shares the DB with the mautrix crypto store; ingest tokens
              stored as SHA-256, admin password as argon2id
  ingest/     gotify/, alertmanager/, gitea/, slack/ payload parsing + formatting
  chart/      Prometheus range-query → PNG chart rendering (go-charts v2)
  config/     viper config, MATRIX_NOTIFIER_* env overrides
  logging/    slog + charmbracelet handler, logger-in-context
  metrics/    Prometheus collectors
  notify/     Notification struct + Sender interface
proto/notifier/v1/      admin.proto — the API source of truth
gen/                    generated stubs — NEVER edit, `make proto` regenerates
ui/                     Vue 3 + Vite + Bootstrap (dark); embedded via
                        go:embed all:dist — StatusPanel/ChannelsPanel/TokensPanel
dev/                    dev stack: docker-compose, bootstrap.sh, cmdclient
test/integration/       testcontainers Synapse E2E (build tag `integration`)
```

## Building (gotchas first)

- **Always `-tags goolm`** on every go build/test/run — libolm (cgo) is
  mautrix's default; the Makefile handles it, raw `go test ./...` breaks.
- **UI changes are NOT picked up by `make build`** if `ui/dist` already
  exists (`ui-ensure` only builds when missing). After touching `ui/src`,
  run `make ui && make build`.
- **Proto changes**: edit `proto/notifier/v1/admin.proto`, run `make proto`
  (buf via `go run`, no global installs). Connect JSON field names are
  camelCase of the proto fields.

```sh
make build            # ui-ensure + go build → bin/matrix-notifier
make test             # go test -tags goolm ./...
make test-integration # real Synapse via testcontainers (needs Docker)
cd ui && npm test     # vitest
golangci-lint run --build-tags goolm   # CI runs this; keep it at 0 issues
```

Full CI gate = UI build + vitest, go build/vet, golangci-lint, buf lint,
`go test -race`.

## Dev stack lifecycle

Requires Docker, jq, Go, Node. Everything on localhost:

| Service       | URL                     | Credentials              |
|---------------|-------------------------|--------------------------|
| Synapse       | http://localhost:8008   | server_name `localhost`  |
| Element Web   | http://localhost:8009   | `admin` / `admin`        |
| synapse-admin | http://localhost:8010   | `admin` / `admin`        |
| Prometheus    | http://localhost:9090   | —                        |
| Postgres      | localhost:5432          | `synapse`                |
| Bot UI/API    | http://localhost:8686   | password `dev-admin-token` |

```sh
make dev-up     # bootstrap.sh: containers, accounts, encrypted room with
                # canonical alias #notifications:localhost, config.dev.yaml
make run-dev    # build + run bot in FOREGROUND (blocks — see below)
make dev-seed   # channel "notifications" + ingest token (bot must be running;
                # prints the mn_... plaintext token)
make dev-down   # stop containers, keep state
make dev-nuke   # scorched earth: containers+volumes, synapse keys, room id,
                # config.dev.yaml, bot data/ — dev-up rebuilds from zero
```

Dev accounts: bot `@notifier:localhost` / `notifier-dev-password`, room ID
cached in `dev/.room_id`.

**Running the bot as an agent** (run-dev blocks): build, then

```sh
nohup ./bin/matrix-notifier --config config.dev.yaml > <scratchpad>/bot.log 2>&1 &
# wait for readiness:
curl -sf http://localhost:8686/health   # {"health":"green"}
```

Before starting, check nothing stale holds the port:
`lsof -i :8686 -sTCP:LISTEN` — a bot from a previous session can survive a
`dev-nuke` and keep 8686 busy with a dead config. Kill it.

## Exercising the bot

**Ingest** (token from `make dev-seed`):

```sh
curl -X POST 'http://localhost:8686/message?token=mn_...' \
  -F title='Hello' -F message='**It works!**' -F priority=5
# also: POST /alertmanager?token=..., POST /gitea?token=... (X-Gitea-Event header)
```

**GOTCHA — adding a new ingest endpoint takes TWO registrations:** the gin
route in `internal/server/server.go` AND the path whitelist in
`cmd/matrix-notifier/main.go` (the outer mux that splits admin API / ingest /
embedded UI). Miss the second and the route silently falls through to the
SPA handler: 200 + index.html, no delivery. Server-level tests exercise
server.New directly and CANNOT catch this — verify new endpoints against
the running dev-stack binary.

`POST /slack?token=...` is Slack-incoming-webhook compatible (token kind
`slack`): JSON body or legacy `payload=` form field; `text` + `blocks`
(header/section) + `attachments`; `username` → title; attachment color
danger/warning → priority 5/4; mrkdwn links/entities converted to markdown;
responds with Slack's literal `ok`. Built for TrueNAS SCALE alert services
and friends, which can't set headers — hence token-in-URL.

Gitea/Forgejo ingest auth: token via `?token=`, `X-Gotify-Key`, or
`Authorization: Bearer` (Forgejo's webhook "Authorization Header" field);
the Forgejo webhook Secret/HMAC signature is ignored by design. The parser
also handles the Forgejo-only (>= v12) `action_run_failure`/`_recover`/
`_success` CI events — repo/link live INSIDE the payload's `run` object
(not top-level `repository`); failure formats at priority 5, recover and
success at 3. Failure-only delivery is configured Forgejo-side ("Custom
Events" → Action Run Failure), no receiver-side filtering.

**Admin API** (plain Connect JSON, camelCase). JWT-only: `Login` is the sole
RPC that accepts the password; everything else needs the JWT (Bearer header,
or the httpOnly cookie browsers get). Login is rate-limited (burst 5, then
1 per 2s) — space out scripted logins:

```sh
JWT=$(curl -s -X POST http://localhost:8686/notifier.v1.AdminService/Login \
  -H 'Content-Type: application/json' -d '{"password": "dev-admin-token"}' | jq -r .token)
curl -s -X POST http://localhost:8686/notifier.v1.AdminService/ListChannels \
  -H "Authorization: Bearer $JWT" -H 'Content-Type: application/json' -d '{}' | jq .
# Other RPCs: GetStatus, ListRooms, CreateChannel {name, roomId, chart},
# UpdateChannel, DeleteChannel, LeaveRoom, ListTokens, CreateToken
# {name, kind, channel, prefix}, UpdateToken, DeleteToken,
# SendTestNotification {channel}, TestToken {name}, Logout,
# ChangeAdminPassword {currentPassword, newPassword} (rotates the JWT
# secret: kills ALL other sessions, returns a fresh token for the caller)
```

Auth model: the admin password lives in the DB (`admin_credentials`, single
row, argon2id hash + JWT signing secret). Config `admin_token_hash` only
SEEDS it on first boot — after that the DB wins and the config value is
inert. The UI stores nothing: the JWT rides in an httpOnly SameSite=Strict
cookie set by the server.

**cmdclient** — standalone mautrix E2EE client (logs in as `@admin:localhost`,
sends one encrypted message, prints decrypted replies). This is how you test
`!notify` commands through real encryption from the CLI:

```sh
go run -tags goolm ./dev/cmdclient -room "$(cat dev/.room_id)" -message '!notify status'
go run -tags goolm ./dev/cmdclient -room "$(cat dev/.room_id)" -verify   # SAS: asserts mutual cross-signing
```

Flags: `-homeserver -user -password -room -message -db -wait -verify
-target -reset-cross-signing`. Its crypto store is `dev/cmdclient.db`
(recovery key beside it as `dev/cmdclient.db.recovery.key`).

**Room commands** (any joined room, encrypted messages only, no backfill):
`!notify ping | status | test | help`.

**Verification in the browser**: log into Element (localhost:8009) as
admin, open the room, "Verify user" on the bot — it auto-accepts SAS.

## Troubleshooting

- **`olm account is marked as shared, keys seem to have disappeared`** —
  something logged out the bot's device. Known cause: Synapse admin
  `PUT /_synapse/admin/v2/users/<id>` with a password logs out all devices
  unless `"logout_devices": false` (bootstrap.sh passes it). Fix: point the
  bot at an empty crypto store; with `data_dir/recovery.key` intact it
  re-verifies automatically as a new device.
- **Bot process exits on fatal sync error** (e.g. M_UNKNOWN_TOKEN) — by
  design; just restart it, it self-heals.
- **"room is not encrypted" on a room that is** — stale state store; the
  bot refetches `/state` automatically at send time. If creating a channel
  by `#alias`, the alias is resolved once and the ID is stored (all internal
  lookups are ID-keyed).
- **Element never shows the green shield** — your Element session holds an
  old cross-signing identity. Verify the Element session with the current
  Security Key (`dev/cmdclient.db.recovery.key` for the dev admin) or reset
  the cryptographic identity in Element, THEN verify the bot.
- **Port 8686 already in use / UI shows stale data** — stale bot process
  from before a nuke: `lsof -i :8686 -sTCP:LISTEN`, kill it. Remember it
  also means the running binary predates your code changes.
- **UI change not visible** — you forgot `make ui` before `make build`
  (embedded dist was stale), or you're hitting the stale process above.
- **Chart layout iteration** —
  `CHART_SAMPLE_OUT=/tmp/x.png go test -tags goolm -run TestRenderLayoutSample ./internal/chart/`.
- **Admin password lost / 401 on everything** — the DB credential is
  authoritative, changing `admin_token_hash` in config does nothing after
  first boot. Recovery: delete the `admin_credentials` row (`DELETE FROM
  admin_credentials;`) and restart — it re-seeds from the config hash. A
  password change also rotates the JWT secret, so 401s right after one are
  just dead sessions: log in again.
- **Bot logs** — stderr (slog + charm handler); mautrix internals logged at
  DBG. When running detached, tail the nohup log file.
- **Health** — `GET /health` is real: 503 with a reason when sync age > 90s
  or not logged in. `GET /metrics` has `matrix_notifier_sync_age_seconds`,
  delivered/failed counters per channel/kind.

## Identity model (don't break it)

`data_dir/recovery.key` is the bot's identity anchor (SSSS recovery key);
`pickle.key` encrypts the crypto store. DB loss is recoverable with the
recovery key (automatic); recovery-key loss requires
`--reset-identity` (destructive: logs out devices, new cross-signing keys,
everyone re-verifies). Never put `--reset-identity` in a restart loop.

## Production (context, not for agents to touch casually)

Prod runs at https://notifier.lil.maurice.fr (`@notifier:matrix.maurice.fr`),
deployed from `~/git/ansible-basics` (role `matrix_notifier`, host
synapse.lil.maurice.fr) with secrets in Vault `prod/kv/matrix-notifier`.
Image: `ghcr.io/thomas-maurice/matrix-notifier:latest` (GHA on master push,
linux/amd64 only). Ask before doing anything prod-facing.
