# Step 5 — Production Packaging, Sprite Deployment Guide & End-to-End Verification

- **Status:** Completed
- **Related Plan:** [`doc/initial-plan.md`](./initial-plan.md)
- **Deployment target change:** the plan assumed a VPS with Docker Compose and MinIO. The event now runs on a **Fly.io Sprite** (persistent VM, sleeps when idle, wakes on HTTP). Docker Compose stays as a documented alternative.

## 1. Objectives
1. ~~Create a multi-stage production `Dockerfile`~~ → done, plus a Docker-free Sprite deploy as the main path.
2. ~~Finalize `docker-compose.yml`~~ → done (secrets now required, storage backend selectable).
3. Verify end-to-end flows on the live deployment and document deployment in `README.md`.

## 2. Research Summary (why the Sprite path looks like this)
- Sprites run Ubuntu with a 100 GB durable ext4 disk, 8 vCPUs, and an HTTPS URL routed to port 8080.
- **Services** (`sprite-env services create … --http-port 8080`) are restarted on crash and on every wake, and the proxy starts them when a request arrives.
- **Docker inside a Sprite** is fiddly: community reports cgroup errors, a small default Docker disk, and slow I/O. The app runs as a native binary instead.
- **MinIO is unnecessary:** the Sprite disk is already durable, so photos go to a local folder.
- **Limits:**
  - no custom domains (the QR carries the `*.sprites.app` URL)
  - no region choice
- **Header test on the live Sprite (temporary echo service):**
  - the proxy sends `Sprite-Client-Ip` (real client) and `X-Forwarded-Proto: https`
  - `X-Forwarded-For` **starts with whatever the client sent**: a spoofed `1.2.3.4` came through first
  - so the per-IP limit and PIN lockout must not trust X-Forwarded-For on a Sprite

## 3. Detailed Change Log

### Backend
- **`backend/storage/filesystem.go` (new):** `FSObjectStore` implementing `PhotoObjectStore` on local disk.
  - Writes go to a temp file in the same folder, then fsync and rename, so a crash or pause never leaves a half-written photo.
  - Rejects path traversal (`..`, empty or root keys).
  - Deleting a missing file is a no-op.
- **`backend/storage/filesystem_test.go` (new):** round trip, no temp leftovers, idempotent delete, traversal rejection.
- **`backend/config/config.go`:**
  - `STORAGE_BACKEND` (`fs` | `minio`, default `minio` so the existing compose setup is unchanged)
  - `PHOTO_DIR`
  - `CLIENT_IP_HEADER`
- **`backend/handlers/identity.go`:** `SetTrustedClientIPHeader`. When set, `ExtractClientIP` uses that header first and falls back to the old X-Forwarded-For / X-Real-IP / RemoteAddr logic.
- **`backend/handlers/identity_test.go` (new):** the trusted header beats a spoofed X-Forwarded-For; falls back when absent.
- **`backend/main.go`** (restructured):
  - Picks the storage backend at startup. MinIO keeps its retry loop.
  - `http.Server` with `ReadHeaderTimeout` and no write timeout (SSE and the ZIP export are long-lived).
  - Graceful shutdown on SIGTERM/SIGINT, so service restarts close SQLite cleanly.
  - Warns at startup when `HOST_PIN` is still the default `2026`, and says when the admin panel is disabled.
  - Static files: `/assets/*` (fingerprinted by Vite) get `Cache-Control: public, max-age=31536000, immutable`; the SPA fallback `index.html` gets `no-cache`.
  - **CORS is now opt-in** via `CORS_ORIGINS`. It used to reflect any `Origin` with credentials allowed. Production is same-origin and Vite dev uses a proxy, so nothing needs it by default.

### Sprite deployment kit (`deploy/sprite/`, new)
- **`deploy.sh`:** one command, safe to re-run.
  - Builds the frontend and a static linux/amd64 binary (`CGO_ENABLED=0`, pure-Go SQLite).
  - Tars and uploads via `sprite exec --stdin`, with macOS xattrs stripped.
  - Swaps `app` atomically and keeps `app.previous` for rollback.
  - First deploy only: creates `photo-share.env` (mode 600) with a random `DEVICE_SECRET` and `HOST_PIN` (or `HOST_PIN=…` from the caller).
  - Creates or restarts the `photo-share` service with `--http-port 8080`.
  - Makes the URL public, then health-checks both `localhost` and the public URL, with retries because `sprite exec` connections occasionally drop with `EOF`.
- **`run.sh`:** service entry point. Loads the env file and `exec`s the binary in the foreground.
- **`photo-share.env.template`:**
  - `STORAGE_BACKEND=fs`
  - paths under `/home/sprite/photo-share`
  - `CLIENT_IP_HEADER=Sprite-Client-Ip`
  - placeholder secrets
  - first-boot event defaults
- **`e2e.mjs`:** live end-to-end check through the public URL using only the HTTP API. It deletes everything it creates and restores the settings it changed.

### Docker alternative
- **`Dockerfile` (new):** multi-stage build (`node:24-alpine` → `golang:1.27-alpine` → `alpine:3.22`), runs as a non-root user.
- **`.dockerignore` (new).**
- **`docker-compose.yml`:**
  - `STORAGE_BACKEND` (default `minio`) and `CLIENT_IP_HEADER` added
  - `DEVICE_SECRET` and `HOST_PIN` are now **required** (`${VAR:?…}`) instead of falling back to public defaults
  - the container port is fixed at 8080
- **`.env.example`:** new variables documented, placeholder secrets instead of usable defaults.

### Docs
- **`README.md` (new):**
  - Sprite deploy, on-Sprite layout, everyday commands
  - backups and rollback
  - event-day run of show
  - Sprite caveats
  - Docker alternative, local dev, tests
  - configuration reference

## 4. Deployment State
- **Sprite:** `photowall`, Ubuntu 26.04, x86_64. It replaced the first test Sprite `my-first-sprite`, which was destroyed: Sprites can't be renamed, and the URL is `<name>-<org suffix>.sprites.app`.
- **URL:** `https://<sprite name>-<org suffix>.sprites.app` (public). Pages: `/` (join), `/<room>`, `/<room>/wall`, `/admin`, `/admin/<room>` (see step 7).
- **Service:** `photo-share` on port 8080. Data in `/home/sprite/photo-share/data`.
- **Admin PIN:** stored in `/home/sprite/photo-share/photo-share.env`, copied from the first Sprite so the PIN stayed the same.
- **Checkpoint:** `v2` "first deploy on photowall (no events yet)".

## 5. Verification Results
- `cd backend && go vet ./... && go test ./...`: all packages pass, including the new filesystem store and client-IP tests.
- `docker compose config` validates. `docker build` succeeds; the container started with `STORAGE_BACKEND=fs` served `/api/health`, `/wall`, and accepted an upload.
- **Deploy script:** first run created the env file and service. Second run kept the env file and restarted the service. Both health checks passed through the public URL.
- **Live end-to-end (`e2e.mjs`, run from inside the Sprite so traffic goes out and back through the Sprites proxy): 19/19 passed.**
  - `/`, `/wall`, `/admin` return the app; assets are served with immutable caching.
  - An upload reaches an open SSE stream immediately (no proxy buffering).
  - The 5-photo limit holds (6th upload gets `429 DEVICE_QUOTA_EXCEEDED`); image bytes come back intact.
  - Reactions, settings changes, hide, and spotlight all arrive over SSE.
  - The admin cookie arrives `Secure; HttpOnly; SameSite=Strict` (the proxy sends `X-Forwarded-Proto: https`).
  - Resetting a phone's limit lets that phone upload again.
  - The album ZIP downloads (`PK` header, `graduation-album-…zip`).
  - Cleanup left 0 test photos.
- **Client IP:** an upload sent with a spoofed `X-Forwarded-For: 9.9.9.9` was stored with the `Sprite-Client-Ip` address instead.
- **Persistence:** after `sprite-env services restart photo-share`, the photo is still listed and its image still serves from disk.
- **Not verified from here:**
  - **The development network reset TLS to `*.sprites.app`**, so no browser or curl check ran from this laptop.
  - A true cold wake was not observed: every `sprite exec` wakes the Sprite.
  - Both need the rehearsal from a phone on mobile data and on the venue Wi-Fi (see the README run of show).
