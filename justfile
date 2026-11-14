# Local overrides (gitignored .env), e.g. NUDGE_URL for `worker-deploy`
set dotenv-load

default:
    just --list

# Install workspace dependencies (worker + web)
install:
    pnpm install

# Build everything: worker bundle, web app, server binary
build: worker-build web-build server-build

# ---- Server ----
# Run PocketBase locally (schema applied on start; data in server/pb_data, mail in server/mail)
server-dev:
    cd server && go run . serve --http 127.0.0.1:8090

# Build the server binary to server/roost
server-build:
    cd server && go build -o roost .

# Re-parse every stored raw message with the current parser (keeps read/star/thread)
server-reparse:
    cd server && go run . reparse

# Create the mail client's sign-in user (or reset its password) in the dev database
server-user email password:
    cd server && go run . user upsert {{email}} {{password}}

# Ingest local .eml files or directories into the dev database (e.g. the parser fixtures)
server-import +paths:
    cd server && go run . import {{paths}}

# Seed the dev database with the parser fixtures
server-seed: (server-import "internal/mailparse/testdata")

# Write the dev database's collections back to the embedded schema snapshot
server-schema-export:
    cd server && go run . schema export > internal/store/schema.json.tmp && mv internal/store/schema.json.tmp internal/store/schema.json

# ---- Worker ----
# Run the Email Worker locally (wrangler dev)
worker-dev:
    cd worker && pnpm run dev

# Regenerate worker-configuration.d.ts from wrangler.jsonc
worker-types:
    cd worker && pnpm run types

# Bundle the Worker without deploying (dry run, output in worker/dist)
worker-build:
    cd worker && pnpm run build

# Deploy the Email Worker to Cloudflare
worker-deploy:
    cd worker && pnpm exec wrangler deploy ${NUDGE_URL:+--var=NUDGE_URL:$NUDGE_URL}

# ---- Web ----
# Run the PWA dev server (Vite; proxies /api to `just server-dev` on :8090)
web-dev:
    cd web && pnpm run dev

# Build the PWA (static SPA, output in web/build)
web-build:
    cd web && pnpm run build

# Build the PWA and copy it into server/pb_public so PocketBase serves it
web-bundle: web-build
    rm -rf server/pb_public && cp -R web/build server/pb_public

# Add shadcn-svelte components, e.g. `just web-ui card dialog`
web-ui +components:
    cd web && pnpm dlx shadcn-svelte@latest add -y {{components}}

# ---- Checks & tests ----
# Run before every commit: type-check, lint, test, then format only if all pass
check: typecheck lint test fmt

# Format sources in place (oxfmt for TS/JS/JSON/CSS, gofmt for Go)
fmt:
    pnpm exec oxfmt
    cd server && gofmt -w .

# Type-check worker and web
typecheck:
    pnpm -r check

# Lint worker and web (oxlint, warnings fail) and vet the server
lint:
    pnpm exec oxlint --deny-warnings
    cd server && go vet ./...

# Run unit tests for all packages and the server
test:
    pnpm -r test
    cd server && go test ./...

# ---- Docker ----
# Build the production image (web app + server)
docker-build tag="roost:latest":
    docker build -t {{tag}} .

# Build amd64 + arm64 and push (or run the Image workflow on GitHub)
docker-push image="ghcr.io/gera2ld/roost:latest":
    docker buildx build --platform linux/amd64,linux/arm64 -t {{image}} --push .

# Run the image locally with data in the roost-data volume (env from server/.env if present)
docker-run tag="roost:latest":
    docker run --rm -p 127.0.0.1:8090:8090 -v roost-data:/data $(test -f server/.env && echo --env-file server/.env) {{tag}}
