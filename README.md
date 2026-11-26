# photo-share: Graduation Live Photo Wall

Guests scan a QR code, send up to 5 photos from their phone, and the photos land live on a projector wall with a
Polaroid "developing" reveal and floating emoji reactions. One host runs any number of **events**: each has its own
6-character room code, wall, photos, per-person limit, and album ZIP.

| Page | Who | What |
|---|---|---|
| `/` | Guests | Type a room code to join an event |
| `/<room>` | Guests (phones) | Name, then camera or upload, then caption; emoji reactions |
| `/<room>/wall` | Projector | Live Polaroid wall, spotlight reveal, QR code + room code card |
| `/admin` | Host (PIN) | List and create events, copy guest links |
| `/admin/<room>` | Host (PIN) | Event settings, open/close uploads, hide/delete/spotlight photos, reset a phone's limit, album ZIP, delete event |

Host flow: open `/admin`, create an event, share the guest link (or show `/<room>/wall` on the projector; its QR
points at `/<room>` and the room code is printed under it).

Stack: Go 1.27 (single binary, SQLite via pure-Go `modernc.org/sqlite`), Svelte 5 + Vite + Tailwind 4,
Server-Sent Events. Photos are stored on local disk (`STORAGE_BACKEND=fs`) or in MinIO (`STORAGE_BACKEND=minio`).

Build history and design notes: [`doc/`](./doc).

---

## Deploy to a Fly.io Sprite (recommended)

A [Sprite](https://docs.fly.io/sprites/) is a persistent Linux VM that sleeps when idle and wakes on the next
HTTP request. The app runs as a Sprite **service** (restarted on crash and on every wake), photos and SQLite live
on the Sprite's durable disk, and the Sprite URL gives you HTTPS. No Docker or MinIO is involved.

### Prerequisites

- The `sprite` CLI, logged in (`sprite login`), and a Sprite selected in this folder (`sprite use <name>`).
- Go 1.27+ and Node 24+ locally (the script builds here and uploads the result).

### Deploy

```bash
./deploy/sprite/deploy.sh                 # uses the sprite from ./.sprite
./deploy/sprite/deploy.sh my-photo-wall   # or name one
HOST_PIN=482913 ./deploy/sprite/deploy.sh # choose the admin PIN on the very first deploy
```

The script:

1. builds the frontend and a static `linux/amd64` binary,
2. uploads them to `/home/sprite/photo-share/app` (the previous release is kept in `app.previous`),
3. on the **first** deploy writes `/home/sprite/photo-share/photo-share.env` with a random `DEVICE_SECRET`
   and `HOST_PIN` (printed once),
4. creates or restarts the `photo-share` service on port 8080,
5. makes the Sprite URL public and runs a health check through it.

Re-running it only replaces the app. The database, photos, and env file are never touched.

### Layout on the Sprite

```
/home/sprite/photo-share/
  app/                 current release (binary, dist/, run.sh)
  app.previous/        last release, for a quick rollback
  data/photoshare.db   SQLite (settings, photo metadata)
  data/photos/         uploaded photos
  photo-share.env      settings and secrets (chmod 600)
```

### Everyday commands

```bash
sprite info                                                      # URL and status
sprite exec -- tail -f /.sprite/logs/services/photo-share.log    # live logs
sprite exec -- sprite-env services restart photo-share           # restart after editing the env file
sprite console                                                   # shell (edit photo-share.env here)
sprite exec -- grep HOST_PIN /home/sprite/photo-share/photo-share.env   # forgot the PIN
```

Events are created and edited in `/admin` (title, subtitle, per-person limit, event code, uploads open/closed).
`MAX_PHOTOS_PER_PERSON` and `MAX_PHOTOS_PER_IP` in the env file are the defaults for new events. If the database
still has photos from the single-event version, they are moved into one event (titled from the old settings or
`EVENT_TITLE`) on the first start; the server log prints its room code.

### Backups and rollback

```bash
sprite checkpoint create --comment "before event"   # snapshot disk (photos + DB + app)
sprite checkpoint list
sprite restore v3                                   # roll the whole disk back (destroys later changes)
```

To roll back only the app: `sprite console`, then
`cd ~/photo-share && mv app app.bad && mv app.previous app && sprite-env services restart photo-share`.

The album ZIP from `/admin/<room>` is the guest-facing backup: every visible photo of that event,
`memories.json`, and an offline `index.html`.

### Run of show (event day)

1. **Day before:** deploy, create the event in `/admin`, open `/<room>/wall` on the projector laptop, scan the QR
   with two phones (Wi-Fi and mobile data), upload, react, hide/delete from `/admin/<room>`, download the ZIP. Then
   delete the test photos (or delete the test event and create the real one) and take a checkpoint.
2. **30 minutes before:** open `/<room>/wall` in fullscreen (press **F**). An open wall keeps the Sprite awake, so
   the first guest never waits for a wake-up.
3. **During:** moderate from `/admin/<room>` on a phone or second laptop. **P** on the wall pauses highlights.
   Flip **Uploads open** off when the event ends; the wall stays up.
4. **After:** download the album ZIP, then `sprite checkpoint create --comment "after event"`.
   Once the wall closes the Sprite goes back to sleep and stops costing compute.

### Things to know about Sprites

- **The URL is `https://<sprite name>-<org suffix>.sprites.app`**.
  Custom domains are not supported yet, and a Sprite can't be renamed: for a new name, `sprite create <name>`,
  copy `~/photo-share/photo-share.env` across, and run `./deploy/sprite/deploy.sh <name>`.
- **Region is not selectable.** Sprites are placed near whoever created them. Check latency from the venue during
  the rehearsal.
- **Some networks block `*.sprites.app`.** A network used during development reset TLS connections to
  it. Test the venue Wi-Fi and mobile data before the event.
- **Client IPs** come from the `Sprite-Client-Ip` header (`CLIENT_IP_HEADER` in the env file). The first
  `X-Forwarded-For` entry is client-controlled and is ignored there. This matters for the per-network upload
  ceiling and the admin PIN lockout.
- **Sleep and wake:** an idle Sprite wakes in about 0.1 to 0.5 s (warm) or 1 to 2 s (cold). In-memory state (live
  connections, PIN lockout counters) resets on a cold wake; everything on disk stays.

---

## Alternative: Docker Compose on a VPS (MinIO)

```bash
cp .env.example .env    # set DEVICE_SECRET (openssl rand -hex 32) and HOST_PIN; compose refuses to start without them
docker compose up -d --build
```

This starts MinIO plus the app on port 8080. Put a TLS reverse proxy (Caddy, nginx) in front of it and set
`CLIENT_IP_HEADER` to the header that proxy sets (for example `X-Real-IP`). The host session cookie is marked
`Secure` when the proxy sends `X-Forwarded-Proto: https`. For disk storage instead of MinIO, set
`STORAGE_BACKEND=fs` (photos go to `/app/data/photos` in the `app_data` volume).

---

## Local development

```bash
# backend (disk storage, no MinIO needed)
cd backend && STORAGE_BACKEND=fs HOST_PIN=1234 go run .

# frontend with hot reload, proxying /api to :8080
cd frontend && npm install && npm run dev
```

Tests:

```bash
cd backend && go test ./...
cd frontend && npm run check && npm run build
BASE=https://<sprite-url> PIN=<host pin> node deploy/sprite/e2e.mjs   # live end-to-end check on a throwaway event it deletes afterwards
```

## Configuration reference

| Variable | Default | Notes |
|---|---|---|
| `PORT` | `8080` | Sprites route their URL to 8080 |
| `STORAGE_BACKEND` | `minio` | `fs` or `minio` (the Sprite env file sets `fs`) |
| `PHOTO_DIR` | `./data/photos` | Used by `fs` |
| `SQLITE_PATH` | `./data/photoshare.db` | |
| `STATIC_DIR` | `../frontend/dist` | Built SPA |
| `DEVICE_SECRET` | dev value | Signs phone identity tokens and host sessions. Changing it resets every phone's identity |
| `HOST_PIN` | `2026` | `/admin` PIN. Empty disables the admin panel |
| `CLIENT_IP_HEADER` | empty | Trusted proxy header with the real client IP |
| `CORS_ORIGINS` | empty | Only needed if the frontend is served from another origin |
| `MAX_PHOTOS_PER_PERSON`, `MAX_PHOTOS_PER_IP` | `5`, `150` | Defaults for new events; each event can change them in `/admin/<room>` |
| `EVENT_TITLE`, `EVENT_SUBTITLE`, `EVENT_CODE` | see `.env.example` | Only used to name the event that photos from the single-event version are moved into |
| `MINIO_*` | see `.env.example` | Only for `STORAGE_BACKEND=minio` |
