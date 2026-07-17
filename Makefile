GO_TAGS := goolm

.PHONY: build test run-dev dev-up dev-down dev-nuke dev-seed ui proto

build: ui-ensure
	go build -tags $(GO_TAGS) -o bin/matrix-notifier ./cmd/matrix-notifier

# go:embed needs ui/dist to exist; build the UI once if it never was.
.PHONY: ui-ensure
ui-ensure:
	@test -f ui/dist/index.html || $(MAKE) ui

test:
	go test -tags $(GO_TAGS) ./...

# Integration tests spin up a real Synapse via testcontainers (needs Docker).
test-integration:
	go test -tags "$(GO_TAGS) integration" -timeout 300s ./test/integration/...

# Build the admin UI into ui/dist (embedded into the binary on next build).
ui:
	cd ui && npm install --no-fund --no-audit && npm run build

proto:
	go run github.com/bufbuild/buf/cmd/buf@latest generate

# Create a "notifications" channel (room from dev/.room_id) and an ingest
# token via the admin API. Requires the bot to be running.
dev-seed:
	@ROOM=$$(cat dev/.room_id); \
	JWT=$$(curl -sf -X POST http://localhost:8686/notifier.v1.AdminService/Login \
	  -H 'Content-Type: application/json' -d '{"password": "dev-admin-token"}' | jq -r .token); \
	[ -n "$$JWT" ] || { echo "login failed (bot not running?)"; exit 1; }; \
	curl -sf -X POST http://localhost:8686/notifier.v1.AdminService/CreateChannel \
	  -H "Authorization: Bearer $$JWT" -H 'Content-Type: application/json' \
	  -d "{\"name\": \"notifications\", \"roomId\": \"$$ROOM\"}" > /dev/null \
	  && echo "channel 'notifications' -> $$ROOM" || echo "channel exists"; \
	curl -sf -X POST http://localhost:8686/notifier.v1.AdminService/CreateToken \
	  -H "Authorization: Bearer $$JWT" -H 'Content-Type: application/json' \
	  -d '{"name": "dev", "kind": "any", "channel": "notifications"}' | \
	  python3 -c 'import json,sys; print("ingest token:", json.load(sys.stdin)["plaintext"])' \
	  || echo "token 'dev' already exists"

dev-up:
	./dev/bootstrap.sh

dev-down:
	docker compose -f dev/docker-compose.yml down

# Full reset: containers, volumes, synapse keys, room, bot state.
dev-nuke:
	docker compose -f dev/docker-compose.yml down -v
	rm -rf dev/synapse/data dev/.room_id config.dev.yaml data

run-dev: build
	./bin/matrix-notifier --config config.dev.yaml
