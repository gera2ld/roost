# Roost

A lightweight, self-hosted personal email client for custom domains: one user, any number of catch-all domains.

- `worker/` — Cloudflare Email Worker: spools raw EML to R2 and nudges the backend.
- `server/` — PocketBase used as a Go framework: ingest, sweep, threading, send, API.
- `web/` — SvelteKit (SPA, adapter-static) + shadcn-svelte PWA, served by the backend.
- `docs/` — [architecture](docs/architecture.md) and [configuration](docs/configuration.md): every setting, command and scheduled job.

Mail flows Cloudflare Email Routing → Worker → R2 → backend (on a signed nudge, plus a sweep every 5 minutes). Sending goes through Resend; alerts go to Telegram. See [docs/architecture.md](docs/architecture.md) for how the pieces fit.

## Prerequisites

Accounts and infrastructure:

- A Cloudflare account with every mail domain as a zone (Email Routing and R2 are free tiers).
- A server with Docker, reachable over HTTPS at a hostname you pick (for example `roost.example.com`) through a reverse proxy with TLS. A Cloudflare quick tunnel works for a trial run.
- For sending: a Resend account. For alerts (optional): a Telegram bot from @BotFather.

Local tools: `git`, Node 26 with pnpm 12 (to deploy the Worker; Wrangler comes with the install), and `openssl`. Building Roost yourself needs more; see [Development](#development).

## Quick Start

Pick the backend's HTTPS hostname first; the examples use `roost.example.com` and the mail domain `example.com`. Steps marked **(you)** need a person: a dashboard or a password.

### 1. Prepare Cloudflare

```sh
pnpm install --filter worker
cd worker
pnpm exec wrangler login
pnpm exec wrangler r2 bucket create roost-spool
```

**(you)** In the dashboard, R2 → Manage API tokens: create an "Object Read only" token for `roost-spool`. Keep its access key ID and secret, and your account ID.

### 2. Deploy the Worker

Set `NUDGE_URL` in `worker/wrangler.jsonc` to `https://roost.example.com/api/roost/nudge`, then generate the shared secret (keep it for step 3) and deploy:

```sh
openssl rand -hex 32                          # the nudge secret
pnpm exec wrangler secret put NUDGE_SECRET    # paste it
pnpm exec wrangler deploy
```

Send a domain's mail to the Worker (repeat per domain; this replaces its current catch-all):

```sh
pnpm exec wrangler email routing enable example.com
pnpm exec wrangler email routing rules update example.com catch-all \
  --action-type worker --action-value roost-inbound
```

To try a single address first, route only that one instead:

```sh
pnpm exec wrangler email routing rules create example.com \
  --match-type literal --match-field to --match-value test@example.com \
  --action-type worker --action-value roost-inbound
```

### 3. Deploy the backend

On the server, in a new directory:

```sh
curl -fsSLO https://raw.githubusercontent.com/gera2ld/roost/main/compose.yml
```

Create `roost.env` next to it:

```sh
ROOST_R2_ENDPOINT=https://<account-id>.r2.cloudflarestorage.com
ROOST_R2_ACCESS_KEY_ID=...
ROOST_R2_SECRET_ACCESS_KEY=...
ROOST_NUDGE_SECRET=...                        # the secret from step 2
ROOST_PUBLIC_URL=https://roost.example.com
```

```sh
docker compose up -d
```

Point an HTTPS reverse proxy for `roost.example.com` at `127.0.0.1:8090`. Check: `curl https://roost.example.com/api/roost/health` returns `{"status":"ok"}`.

### 4. Initialization

**(you)** Create your sign-in (there is no signup page):

```sh
docker compose exec roost /app/roost user upsert you@example.com 'a long password'
```

To sign in with Google, GitHub or another OAuth2 provider instead, see [Sign-in](docs/configuration.md#sign-in). Sign in at `https://roost.example.com` and send a message to an address on your domain from another account. It shows up within seconds (`pnpm exec wrangler tail roost-inbound` shows the Worker side).

### 5. Sending and alerts

- **(you)** In Resend, verify each sending domain (SPF, DKIM, DMARC), create an API key, and add the webhook `https://roost.example.com/api/roost/resend/webhook`.
- Optional: create a Telegram bot with @BotFather and get your chat ID.

Add to `roost.env`, then reload with `docker compose up -d --force-recreate`:

```sh
ROOST_RESEND_API_KEY=re_...
ROOST_RESEND_WEBHOOK_SECRET=whsec_...
ROOST_TELEGRAM_BOT_TOKEN=123456789:AA...      # the whole token
ROOST_TELEGRAM_CHAT_ID=...
```

Check alerts with `docker compose exec roost /app/roost notify test`. For alerts when the Worker cannot store a message, set the same two Telegram values as Worker secrets (`wrangler secret put TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID`).

Finally, in the app's **Settings**, turn sending on for each domain Resend verified (all start receive-only) and add an identity with a display name and signature for each address you send from.

Every setting, command and scheduled job is in [docs/configuration.md](docs/configuration.md).

## Development

Needs [`just`](https://github.com/casey/just), Go 1.27 and Docker with `buildx` on top of the tools above. Commands live in the [justfile](justfile) (`just` lists them); [AGENTS.md](AGENTS.md) has the conventions.

```sh
just install && just check        # typecheck, lint, test, format; run before every commit
just server-dev                   # API and built app on http://127.0.0.1:8090
just web-dev                      # PWA with hot reload on http://localhost:5173
just docker-build                 # local image roost:latest
just docker-push <registry>/roost:latest   # amd64 + arm64, pushed
```
