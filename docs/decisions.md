# Decisions

Why Roost is built the way it is. Each entry gives the choice, the reason, and what it costs. For how the pieces fit, see [architecture.md](architecture.md). When a decision changes, update its entry and say what replaced what.

## Scope

**A mail client, not a mail server.** Roost reads and sends mail for one person through its own PWA. There is no IMAP, POP or SMTP endpoint, no team inbox and no Gmail feature parity. Cloudflare receives the mail and Resend sends it, so there is no mail server to run. The cost: desktop clients such as Thunderbird can't connect.

**One user.** The single account is created from the CLI and there is no signup. This keeps auth and every rule simple. OAuth2 sign-in links to that one account by email and never creates another.

## Where mail lives

**Your own SQLite and disk, not Gmail.** Gmail as the system of record would mean the Gmail API, whose OAuth has refresh-token and app-verification limits, and sending through Gmail's third-party "Send as", which ends in January 2027. Instead, mail lives on a server you run, and outbound goes straight through Resend. The data is a database file and a folder of raw messages that you own and can copy with any tool.

**Raw messages on disk, only the index in SQLite.** Each message is written once as its original `.eml` file. SQLite holds what the app queries: headers, text body, threads, flags and attachment metadata. Attachments are cut out of the `.eml` when downloaded. The database stays small, no attachment is stored twice, and a parser fix reaches old mail with `roost reparse`.

**No migrations.** `schema.json` is the whole schema and is applied in full on every start, with a round-trip test that keeps it canonical. One file describes the database, and there is no migration history to replay. The cost: data in a removed field is deleted on the next start. The users collection's sign-in settings are the one exception: they are kept on start (or set from the environment) so they can be managed in the admin UI without client secrets entering the repo.

**Search with an FTS5 trigram index.** Chinese and Japanese text has no spaces between words, so word tokenizers miss most searches. Trigrams match any substring of three or more characters; shorter terms fall back to a plain `LIKE`.

## Receiving

**Cloudflare Email Routing and a Worker.** Every domain's catch-all goes to one Worker, so any address on any domain lands in one inbox. The address a message was sent to, which a catch-all or Bcc doesn't reliably put in the headers, is kept from the envelope.

**Spool to R2 first, then tell the backend.** The Worker's only required job is to write the raw message to R2. It then sends a best-effort, signed nudge, and a sweep every 5 minutes picks up anything the nudge missed. Posting mail straight to the backend would lose it whenever the backend is down; with the spool, downtime only delays mail. R2 rather than KV, because KV is eventually consistent and poorly suited to large objects.

**No forwarding safety net.** An early plan forwarded a copy of every message to an old mailbox for the first months. Instead, the Worker retries the R2 write twice; if it still fails, it alerts Telegram directly and throws, so the message is not accepted silently. This is one path to trust rather than two, and the alert reaches you even when the backend is down.

**Ingest is idempotent.** It dedupes on the R2 key, then on Message-ID, so the nudge and the sweep can overlap safely, and one message sent to two of your addresses stays one message.

**A public HTTPS nudge, no tunnel.** The Worker reaches the backend over the internet anyway. The nudge carries an HMAC signature with a 5-minute window, and a replay only re-runs an idempotent ingest, so a tunnel or Cloudflare Access adds setup without adding much safety. Access stays available as an option.

## Backend

**PocketBase as a Go framework.** It brings the REST API, auth, an admin UI and realtime updates. Roost's own logic (ingest, threading, sending) is plain Go with enmime for MIME and charsets, rather than PocketBase's JS hooks, so moving to plain Go and SQLite later would stay cheap.

**Reads through PocketBase, writes through Roost routes.** The PWA reads through PocketBase's record API, but every write goes through `/api/roost/*`, and collections reject direct writes. Invariants such as purge order and derived thread counts are enforced in one place.

**Threads by headers first.** In-Reply-To and References decide threads, including a parent that arrives late. The subject is used only for `Re:` or `Fwd:` mail without those headers, within 30 days and with a shared participant, so unrelated "Re: hello" messages stay apart.

## Sending

**Resend.** It sends from your domains with SPF and DKIM over a plain HTTPS API, called only from the server. Amazon SES could replace it if volume or cost ever justify the move.

**Roost keeps its own copy of sent mail.** Resend takes JSON, not raw MIME. Roost builds the full message itself and stores it like received mail, so sent mail is searchable, threaded and downloadable through the same paths. Bcc stays in the database only. Resend (through Amazon SES) replaces the Message-ID Roost sets, so the message takes the delivered ID from the `email.sent` webhook; replies quote that one. The raw copy keeps Roost's ID, which is harmless because sent mail is never reparsed. The cost: without the webhook, replies from other mail clients thread only by the subject fallback.

**Never send twice, never look unsent.** Each send uses the record ID and Message-ID as Resend's idempotency key, so a retry after a timeout can't send a duplicate. If Resend accepted a message but saving the result fails, the message is still marked sent.

**Domains start receive-only.** Receiving on a domain doesn't mean Resend has verified it. Sending is turned on per domain in Settings, and the server refuses everything else.

## Reading

**Plain text only.** All inbound mail is untrusted. The PWA shows text: HTML-only mail is converted at ingest, and nothing loads remote images or runs email HTML. Attachments are always downloads with sanitized names. Rich HTML would need a strict sanitizer and a sandboxed frame, and is left for later.

**One origin.** The backend serves the PWA from the same host as the API, so the CSP and auth stay simple. The service worker caches the app shell only, never `/api`.

## Operations

**Telegram alerts are best effort.** Ingest only queues an alert, so a Telegram outage never delays or fails mail. Bursts collapse into one summary so a catch-up after downtime doesn't flood your phone. Pipeline problems (R2 unreachable, stuck spool, no mail for days) alert once, repeat daily and send an all-clear. Alerts that are too noisy stop being read.

**No backup code.** Everything lives in one `/data` volume, so any backup tool works. The R2 lifecycle rule that expires the spool is turned on only once you are making copies of that volume, since until then R2 is the only second copy of new mail.
