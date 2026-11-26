# Step 7 — Multiple Events (Rooms)

- **Status:** Done and tested locally. **Not deployed to the Sprite yet.**
- **Decisions (from the host):**
  - one master admin (`HOST_PIN`) manages every event
  - room IDs are random 6-character codes
  - `/` is a "join a room" page

## 1. Flow
```
host: /admin → create event → gets room code (e.g. k7p2qx) + guest link
guests: scan QR on /k7p2qx/wall  or  open /k7p2qx  or  type k7p2qx at /
```

| URL | Who | What |
|---|---|---|
| `/` | guest | type a room code (checked before leaving the page) |
| `/<room>` | guest | upload flow from step 6, scoped to the event |
| `/<room>/wall` | projector | wall for that event only; QR points at `/<room>`; room code printed under the QR |
| `/admin` | host | event list (photo counts, created time, copy guest link, open wall, manage) + create form |
| `/admin/<room>` | host | the step 4 panel, scoped to the event, plus: guest link + copy, uploads open/closed switch, delete event |

## 2. Change Log

### Backend
- **`storage/sqlite.go`** (rewritten):
  - **`events` table:**
    - `id` = room code
    - title, subtitle, event code
    - per-person and per-IP limits
    - `uploads_open`, `created_at`
  - `photos.event_id`, added with `ALTER TABLE` on existing databases. Indexes on (event, created), (event, device), and (event, ip).
  - **Room codes:**
    - 6 characters from `abcdefghjkmnpqrstuvwxyz23456789` (no 0/o/1/l/i), using `crypto/rand`
    - retried on collision; reserved words (`assets`, `health`) are skipped
    - `NormalizeRoomCode` lowercases the code and validates it
  - **Event methods:** `CreateEvent`, `GetEvent`, `ListEvents` (with visible and hidden photo counts), `UpdateEvent`, `DeleteEvent` (removes the photo rows and returns them so the files can be deleted).
  - **Every photo query is scoped by event:** list, per-device list, quota counts, per-IP counts, device reset.
  - **Migration:** photos from the single-event version (`event_id = ''`) are moved into one generated event, titled from the old `settings` table (or `EVENT_TITLE`). The room code is logged. The old `UpdateSettings` and `GetSettings` APIs are gone.
- **`handlers/sse.go`:** the hub is per room (`Subscribe(room)`, `Broadcast(room, …)`, `ServeRoom`). There is a new `room_deleted` event.
- **`handlers/api.go`:**
  - Guest endpoints moved under `/api/rooms/{room}/…` (config, me, photos GET/POST, reactions, events). `withRoom` answers `404 ROOM_NOT_FOUND`. Room codes are case-insensitive.
  - Public config adds `room` and `uploadsOpen`.
  - Uploads to a closed event get `403 UPLOADS_CLOSED`. A wrong event code gets `403 EVENT_CODE_INVALID`.
  - Quota and the per-network limit are counted per event.
  - Photo files are stored under `<room>/<id>.<ext>`.
- **`handlers/admin.go` / `admin_events.go` (new):**
  - `GET/POST /api/admin/events`
  - `GET/PUT/DELETE /api/admin/events/{room}`
  - `GET /api/admin/events/{room}/photos`
  - `POST /api/admin/events/{room}/devices/{id}/reset`
  - `GET /api/admin/events/{room}/export.zip`
  - Photo hide/delete/spotlight stay addressed by photo ID and broadcast to that photo's room.
  - Create and update share one validator (only the fields sent change). The event code never appears in public broadcasts.
  - Deleting an event removes its files and broadcasts `room_deleted`.
- **`handlers/export.go`:** exports one event. File name is `<title-slug>-<room>-album-<date>.zip`.
- **`storage/filesystem.go`:** removes an event's folder once its last photo is deleted.

### Frontend
- **`lib/api.ts`:**
  - Every guest call takes `room`.
  - Event codes are stored per room (`ps_event_code:<room>`).
  - Adds `normalizeRoomCode` and `isRoomNotFound`, plus admin event calls.
- **`lib/sse.ts`:** `connectSSE(room, handlers)`, with a new `onRoomDeleted` handler.
- **`lib/wall.ts`:** `buildUploadTarget(location, room)` builds `<origin>/<room>`, still honouring `?url=` and `?code=`.
- **`App.svelte`:** path router for `/`, `/<room>`, `/<room>/wall`, `/admin`, and `/admin/<room>`. Pages are keyed by room. Garbage paths fall back to the join page with a message.
- **`components/HostGate.svelte` (new):**
  - The PIN login (lockout countdown, unreachable server) pulled out of `Admin.svelte`, so both admin pages share one gate.
  - It provides `useHostSession()` with `expire()` and `logout()`.
- **`routes/Join.svelte` (new):** join card styled as a Polaroid, a large code input, validation, and a "no such event" message.
- **`routes/AdminHome.svelte` (new):**
  - Create form: title required, optional subtitle, per-person limit.
  - Event list with empty, loading, and error states. A newly created event is highlighted. Each row has Copy guest link, Wall, and Manage.
- **`routes/Admin.svelte`:**
  - Takes `room` and uses `HostGate`.
  - Header shows the title and room code, with an "All events" link.
  - New guest link card with copy, an uploads open/closed switch (`role="switch"`), and a "Delete event" section with a confirmation dialog.
  - Shows "Event not found" for an unknown or deleted room, including when it's deleted while the page is open.
- **`routes/Upload.svelte`:**
  - Scoped to `room`.
  - Shows "Event not found" for unknown or deleted rooms.
  - Shows "Uploads are closed" (reactions still work) and updates live when the host flips the switch.
- **`routes/Wall.svelte`:** scoped to `room`. Shows "This event is not here" for unknown or deleted rooms.
- **`components/QrCard.svelte`:** shows the room code under the URL, and the event-code hint uses `/<room>/wall?code=…`.

### Deploy kit / docs
- **`deploy/sprite/e2e.mjs`:** creates a throwaway event, runs every check on it, then deletes it. It never touches real events.
- **`deploy/sprite/deploy.sh`:** the summary now prints the admin, join, and per-room URLs.
- **`deploy/sprite/photo-share.env.template`:** comments updated.
- **`README.md`:** URL table, host flow, run of show, and config reference updated.

## 3. Verification (local)
- **`go vet ./... && go test ./...`: all pass.**
  - Updated: identity, quota, shared Wi-Fi, event code, reactions, login and lockout, moderation, export.
  - New:
    - unknown room is 404
    - room codes are case-insensitive
    - events are isolated: photos, per-event quota, and SSE do not leak between rooms
    - create/list/update events with validation, live config to that room only, and the event code not leaked
    - closing uploads gives 403
    - deleting an event removes its photos and files and broadcasts `room_deleted`; other events are untouched
    - the export contains only that event's visible photos
    - photos from the single-event version are moved into an event
- **`npm run check`: 0 errors, 0 warnings. `npm run build`: succeeded.**
- **Browser run against the real Go server (fresh database), 30/30:**
  - **Join page:** rejects bad codes, rejects unknown codes, and accepts a valid code in any case.
  - **Admin home:**
    - PIN login and an empty state
    - create requires a title
    - creating two events gives two distinct 6-character codes
    - "Copy guest link" copies `<origin>/<room>`
  - **Walls:** each shows its own title, with the room code and `/<room>` URL on the QR card.
  - **Uploads:** a guest upload in A lands on wall A only. The same phone in B keeps its nickname and has a fresh 5-photo quota.
  - **Admin event page:**
    - lists only that event's photos
    - closing uploads flips the open guest page to "Uploads are closed" live, and reopening restores it
    - the ZIP is named for the event
  - **Deleting event B:**
    - removes it from the list
    - the open wall B shows "not here"
    - the open guest page B shows "Event not found"
  - **Bad URLs:** a garbage path goes to the join page, and an unknown admin room shows "Event not found".
  - **Logout** returns to the PIN screen. **No page errors.**
- **`deploy/sprite/e2e.mjs` against the local server: 27/27.**
