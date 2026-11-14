# Configuration

Every setting and operational detail behind the [Quick Start](../README.md#quick-start). For how the pieces fit, see [architecture.md](architecture.md).

## Backend environment

Set these in `roost.env` next to `compose.yml`. Ingest is off without the R2 variables, the nudge route without the secret, sending without the Resend key, and alerts without both Telegram values.

| Variable | Value |
| --- | --- |
| `ROOST_R2_ENDPOINT` | `https://<account-id>.r2.cloudflarestorage.com` |
| `ROOST_R2_BUCKET` | Bucket name (default `roost-spool`) |
| `ROOST_R2_ACCESS_KEY_ID`, `ROOST_R2_SECRET_ACCESS_KEY` | An R2 API token with read and list access to the bucket |
| `ROOST_NUDGE_SECRET` | Shared secret; the same value as the Worker's `NUDGE_SECRET` |
| `ROOST_PUBLIC_URL` | Where you open the app; alerts link to it (optional) |
| `ROOST_RESEND_API_KEY` | Resend key with sending access |
| `ROOST_RESEND_WEBHOOK_SECRET` | Resend webhook signing secret (`whsec_…`) |
| `ROOST_RESEND_BASE_URL` | Resend API override, for local testing only |
| `ROOST_TELEGRAM_BOT_TOKEN` | The whole BotFather token (`123456789:AA…`) |
| `ROOST_TELEGRAM_CHAT_ID` | Chat to alert |
| `ROOST_ALERT_IDLE_DAYS` | Days without new mail before an alert (default 3; 0 turns it off) |
| `ROOST_SPOOL_RETENTION_DAYS` | Your R2 lifecycle age in days (default 30) |
| `ROOST_PASSWORD_AUTH` | `true` or `false`: password sign-in (unset: keep the admin UI setting; see [Sign-in](#sign-in)) |
| `ROOST_OAUTH2_PROVIDERS` | OAuth2 providers, comma-separated, for example `google,github`, or `none` (unset: keep the admin UI setting) |
| `ROOST_OAUTH2_<NAME>_CLIENT_ID`, `ROOST_OAUTH2_<NAME>_CLIENT_SECRET` | Credentials for each listed provider, for example `ROOST_OAUTH2_GITHUB_CLIENT_ID` |
| `ROOST_OAUTH2_<NAME>_AUTH_URL`, `_TOKEN_URL`, `_USER_INFO_URL`, `_DISPLAY_NAME` | Optional per provider; `oidc` and self-hosted providers need the URLs |
| `ROOST_DATA_DIR`, `ROOST_MAIL_DIR` | Database and raw mail folders (the image uses `/data/pb_data` and `/data/mail`) |

## Worker settings

| Name | Kind | Value |
| --- | --- | --- |
| `NUDGE_URL` | Variable in `worker/wrangler.jsonc` | `https://<backend host>/api/roost/nudge` |
| `NUDGE_SECRET` | Secret | Same value as `ROOST_NUDGE_SECRET` |
| `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID` | Secrets, optional | Alert when a message cannot be written to R2 |
| `ACCESS_CLIENT_ID`, `ACCESS_CLIENT_SECRET` | Secrets, optional | Only if Cloudflare Access protects the nudge route |

Set secrets with `pnpm exec wrangler secret put <NAME>` in `worker/`. When developing with `just worker-deploy`, a gitignored `.env` at the repo root can set `NUDGE_URL` instead of editing `wrangler.jsonc`.

## Sign-in

Roost has one user, created with `user upsert`. Password sign-in is on by default; OAuth2 can be added, and the sign-in page shows a button per provider and the password form only while password sign-in is on.

- Set it up in either place. In the admin UI (`/_/`): Collections → `users` → Options → OAuth2 and Password; these settings are kept when the schema is applied on start. Or with the `ROOST_PASSWORD_AUTH` and `ROOST_OAUTH2_*` variables, which replace the admin UI setting they cover on every start.
- Register `https://<backend host>/api/oauth2-redirect` as the redirect URL with the provider.
- The provider account must have the same email as the Roost user. Sign-ins that would create a new user are refused, so OAuth2 never becomes a signup.
- The server does not start if neither password sign-in nor an OAuth2 provider is on. Schema exports leave sign-in settings out, so client secrets never reach `schema.json`.

### Example: Dex only

To sign in only through [Dex](https://dexidp.io) at `https://dex.example.com`, add a client to Dex's config:

```yaml
staticClients:
  - id: roost
    name: Roost
    secret: <a long random string>
    redirectURIs:
      - https://roost.example.com/api/oauth2-redirect
```

Then add to `roost.env` and run `docker compose up -d --force-recreate`:

```sh
ROOST_PASSWORD_AUTH=false
ROOST_OAUTH2_PROVIDERS=oidc
ROOST_OAUTH2_OIDC_CLIENT_ID=roost
ROOST_OAUTH2_OIDC_CLIENT_SECRET=<the same secret>
ROOST_OAUTH2_OIDC_AUTH_URL=https://dex.example.com/auth
ROOST_OAUTH2_OIDC_TOKEN_URL=https://dex.example.com/token
ROOST_OAUTH2_OIDC_USER_INFO_URL=https://dex.example.com/userinfo
ROOST_OAUTH2_OIDC_DISPLAY_NAME=Dex
```

The endpoints are listed at `https://dex.example.com/.well-known/openid-configuration`. Dex must report the email as verified (`email_verified: true`) and it must match the Roost user's email, or the sign-in is refused. `user upsert` still needs a password to create the user, but it can't be used to sign in while password sign-in is off.

## Ingress

The Worker reaches the backend over the public internet, so any HTTPS endpoint works, for example a Caddy or nginx site proxying to `127.0.0.1:8090`. The nudge is HMAC-signed with a 5-minute window and a replay only re-runs an idempotent ingest, so it needs no extra gate.

- To try Roost from a laptop, `cloudflared tunnel --url http://127.0.0.1:8090` prints a temporary `*.trycloudflare.com` URL to use as the nudge host. The 5-minute sweep still picks up mail if the URL changes.
- Optionally, put a Cloudflare Access service-token policy on `/api/roost/nudge` and set the Worker's `ACCESS_*` secrets.

## Resend

- Publish a DMARC record for each sending domain (start with `p=none`), alongside the SPF and DKIM records Resend gives you.
- Subscribe the webhook to `email.sent`, `email.delivered`, `email.delivery_delayed`, `email.bounced` and `email.complained`. `email.sent` matters for threading: it carries the Message-ID Resend actually delivered, which recipients reply to.
- Every domain starts receive-only. Turn sending on per domain in the app's Settings once Resend has verified it.

## Telegram

Create a bot with @BotFather (`/newbot`). Send it any message, then open `https://api.telegram.org/bot<token>/getUpdates` and copy `message.chat.id`. Check the setup with `docker compose exec roost /app/roost notify test`.

## Commands

Run these with `docker compose exec roost /app/roost <command>`.

| Command | Does |
| --- | --- |
| `user upsert EMAIL PASSWORD` | Creates the app sign-in, or resets its password (there is no signup) |
| `superuser upsert EMAIL PASSWORD` | Creates a PocketBase admin for the admin UI at `/_/` |
| `import PATH [--to ADDRESS]` | Imports `.eml` files copied into the volume, for example under `/data/import` |
| `ingest KEY...` | Retries spooled messages by key after fixing what made them fail |
| `reparse` | Rebuilds message rows from the raw mail after a parser change |
| `notify test` | Sends a test alert |

## Scheduled jobs

- **Every 5 minutes** (and at start): the sweep ingests any spooled message the database has not seen.
- **Every 30 minutes**: monitoring checks that the sweep can list R2, that no spooled mail older than 2 hours is missing (it is ingested on the spot when possible), and that mail has arrived within `ROOST_ALERT_IDLE_DAYS`. Each problem alerts once, repeats daily, and sends an all-clear. Bounces and spam complaints on sent mail alert at once.
- **Daily at 03:17**: threads trashed over 30 days ago are purged, and ingest bookkeeping is trimmed for spool objects a week past the R2 lifecycle window.

The schema is applied on every start; there are no migrations to run.

## Limits

Inbound messages over 30 MiB fail without retries (Cloudflare caps mail at about 25 MiB anyway). Attachments carried by a forward are capped per send, and draft bodies at 1 MiB.

## Data

Everything lives in the `/data` volume: `pb_data/` (SQLite) and `mail/` (raw mail, written once).

- SQLite runs in WAL mode. Copy it while the container is stopped (`docker compose stop`, copy, `docker compose start`), from a filesystem snapshot, or with SQLite's online `.backup`. A copy of a live `data.db` without its `-wal` file can be inconsistent. Raw mail files never change, so they are always safe to copy.
- R2 lifecycle rule: in R2 → `roost-spool` → Settings → Object lifecycle rules, delete objects 30 days after upload. Turn it on only once copies of `/data` are being made and you have restored one, because until then R2 is the only second copy of new mail. If you pick another age, set `ROOST_SPOOL_RETENTION_DAYS` to match.
