# syntax=docker/dockerfile:1
# Build stages run on the builder's native arch ($BUILDPLATFORM); Go cross-compiles.

# ---- Web (SvelteKit static SPA) ----
FROM --platform=$BUILDPLATFORM node:26-alpine AS web
RUN npm install -g pnpm@12.18.0
WORKDIR /src
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json web/
COPY worker/package.json worker/
RUN pnpm install --frozen-lockfile --filter web
COPY web/ web/
RUN pnpm --filter web build

# ---- Server (PocketBase as a Go framework) ----
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS server
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
# Pure-Go SQLite (modernc) needs no CGO.
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/roost .

# ---- Runtime ----
FROM alpine:3.21
# CA certs for R2/Resend/Telegram over HTTPS; tzdata for log timestamps.
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -h /app appuser \
 && mkdir -p /data/pb_data /data/mail \
 && chown -R appuser:appuser /data
WORKDIR /app
COPY --from=server /out/roost /app/roost
COPY --from=web /src/web/build /app/pb_public
USER appuser
# All state lives here: pb_data/ (SQLite) and mail/ (write-once raw EML). Back up this volume.
VOLUME /data
# Defaults for every command, so `docker exec … /app/roost user upsert` hits the same database as serve.
ENV ROOST_DATA_DIR=/data/pb_data ROOST_MAIL_DIR=/data/mail
EXPOSE 8090
# Runtime config via -e / --env-file: ROOST_R2_*, ROOST_NUDGE_SECRET (see docs/configuration.md).
ENTRYPOINT ["/app/roost"]
CMD ["serve", "--http=0.0.0.0:8090"]
