# Step 4 — Host Moderation Panel, Configurable Event Settings & ZIP Album Export (`/admin`)

- **Status:** Completed
- **Related Plan:** [`doc/initial-plan.md`](./initial-plan.md)

## 1. Objectives
1. Implement **Host Admin API endpoints** protected by `HOST_PIN`:
   - `PUT /api/admin/config` — Update **Event Title**, **Event Subtitle**, **Max Photos Per Person**, and **Event Code** at runtime and broadcast `config_updated` over SSE.
   - `DELETE /api/admin/photos/:id` (or hide/unhide toggle) — Remove an unwanted photo from MinIO/SQLite and immediately broadcast `photo_deleted` to `/wall`.
   - `POST /api/admin/devices/:device_id/reset` — Reset a specific classmate's upload limit if they made a mistake.
   - `GET /api/admin/export.zip` — Stream a complete `.zip` archive containing all uploaded photos (`001_nickname_id.jpg`), `memories.json`, and a self-contained `index.html` offline Polaroid gallery.
2. Build the **Host Admin UI (`/admin`)** in Svelte:
   - PIN login screen.
   - Live Event Settings form (Title, Subtitle, Per-Person Limit).
   - Photo moderation grid with instant Delete / Trigger Spotlight buttons.
   - One-click **Download Graduation Album (.zip)** button.

## 2. Detailed Change Log

### Host Auth & Admin API (`backend/handlers/admin.go`, new)
- **`HostAuth`**:
  - Constant-time PIN check. An empty `HOST_PIN` disables the panel instead of leaving it open.
  - Signed session token `<expiryUnix>.<hmac>` using `DEVICE_SECRET`. The PIN is part of the signed data, so changing `HOST_PIN` logs every host out.
  - 12h session in the `ps_host` cookie: `HttpOnly`, `SameSite=Strict`, `Path=/api/admin`, `Secure` behind HTTPS.
  - PIN throttling per client IP: 5 wrong PINs within 5 minutes lock that IP for 60s (`429 PIN_LOCKED` + `Retry-After`).
- **`requireHost` middleware**: accepts the session cookie, or the PIN in the `X-Host-Pin` header (for scripts and tests; header attempts count against the same throttle).
- **Routes** (all except login/logout require host auth):
  - `POST /api/admin/login` `{pin}`, `POST /api/admin/logout`, `GET /api/admin/session`.
  - `GET /api/admin/config`: every runtime setting, including `eventCode` and `maxPhotosPerIp`.
  - `PUT /api/admin/config`: partial update (only the fields sent change). Validation errors come back per field (`fields.title`, `fields.eventCode`, ...):
    - title must be 1–80 characters
    - subtitle up to 160 characters
    - per-person limit 1–50
    - event code up to 40 characters, with no spaces or `/?&#` (so it survives inside the QR link)
    - per-IP limit 1–10000

    On success it broadcasts `config_updated` with the **public** config only: the code never leaves the admin API, the wall only learns `requiresEventCode`.
  - `GET /api/admin/photos`: every photo, including hidden ones and `deviceId`.
  - `PATCH /api/admin/photos/{id}` `{hidden}`: hiding broadcasts `photo_deleted`; unhiding broadcasts `photo_added` (without `deviceId`).
  - `DELETE /api/admin/photos/{id}`: removes the SQLite record, then the MinIO object (an object-delete failure is logged; the photo is already off every screen), and broadcasts `photo_deleted`.
  - `POST /api/admin/photos/{id}/spotlight`: broadcasts `photo_spotlight`. Returns `409` for hidden photos.
  - `POST /api/admin/devices/{deviceId}/reset`: wraps `ResetDeviceQuota`. Returns `404` if the phone has no photos, `400` for an already-reset marker.
- **`backend/handlers/api.go`**:
  - Added the `Host *HostAuth` field and initialized it in `NewAPI`.
  - `RegisterRoutes` now also registers the admin routes.
  - `GET /api/config` now uses the shared `publicConfigFrom` helper.

### Graduation Album Export (`backend/handlers/export.go`, new)
- `GET /api/admin/export.zip` streams `graduation-album-YYYY-MM-DD.zip` straight to the response.
  - The ZIP is never built in memory, and the export stops if the browser cancels the download.
- Contents:
  - **`photos/NNN_nickname_<id8>.<ext>`**: visible photos only, oldest first. Nicknames are slugged to ASCII. Images are stored without re-compression (`zip.Store`).
  - **`memories.json`**: title, subtitle, export time, count, and each photo's file, ID, nickname, caption, and time. A `missing` list names any photo whose image could not be read from storage.
  - **`index.html`**: self-contained offline album (inline CSS, `html/template` escaping, tilted Polaroid cards linking to the full image). Times are shown in the viewer's timezone, with UTC as the fallback text.
- Hidden photos are left out of the album.

### Backend Tests (`backend/handlers/admin_test.go`, new)
- `TestAdminLoginSessionAndLockout`: 401 without credentials, cookie flags, session reuse, forged cookie rejected, logout, lockout after 5 wrong PINs (even for the correct PIN).
- `TestAdminConfigUpdateBroadcastsWithoutLeakingCode`: per-field validation errors, a successful update, the event code absent from the SSE frame, partial updates keeping the other fields.
- `TestAdminModerationHideDeleteSpotlightAndReset`:
  - hide/unhide broadcasts, with no device ID in public frames
  - `409` when spotlighting a hidden photo
  - the `photo_spotlight` broadcast
  - a limit reset letting a capped phone upload again
  - delete removing the image (`404`), and a second delete returning `404`
- `TestAdminExportZip`: auth required, file naming, hidden photo excluded, manifest order, HTML escaping.

### Frontend API Client (`frontend/src/lib/api.ts`)
- Added `AdminConfig` and these calls:
  - `adminLogin`, `adminLogout`, `adminHasSession`
  - `fetchAdminConfig`, `updateAdminConfig`, `fetchAdminPhotos`
  - `setPhotoHidden`, `deleteAdminPhoto`, `spotlightPhoto`, `resetDeviceQuota`
  - `ADMIN_EXPORT_URL`
- All use `credentials: 'include'` so the `ps_host` cookie carries the session.

### Host Panel (`frontend/src/routes/Admin.svelte`, rewritten)
- **Session check on load**, then one of these screens:
  - PIN screen with a labelled password field (`inputmode="numeric"`).
  - "Wrong PIN" message, or a lockout countdown that disables the field.
  - "Server could not be reached" screen with a Reload button.
- **Header**: event title, live-update status, "Open the wall" (new tab; includes `?code=` when an event code is set), "Log out".
- **Event settings**:
  - Title (with counter), subtitle (with counter), photos per person, event code.
  - Saves only the fields that changed. Save is disabled until something changes, and "Undo changes" restores the saved values.
  - Shows per-field server errors and a result message. When an event code is set, it shows the exact `/wall?code=…` link the projector must use.
- **Graduation album**: "Download album (.zip)" link that streams the export using the session cookie. It explains what is inside and that hidden photos are left out.
- **Photos**:
  - Filter buttons: All / On the wall / Hidden, with counts.
  - Each card shows:
    - the image and caption, `@nickname`, and relative time
    - "This phone: X of N uploads used"
    - **Spotlight on wall** (disabled while hidden), **Hide/Unhide**, **Delete**
    - **Reset this phone's limit**
  - Delete and reset open a native `<dialog>` confirmation (Escape and Cancel close it). A toast confirms each action.
  - The list refetches on live `photo_added` / `photo_deleted` events and after an SSE reconnect.
  - If the session expires, any action returns the host to the PIN screen with a message.

### Wall & Shell Follow-ups
- **`frontend/src/routes/Wall.svelte`**: a `photo_added` for a photo older than 2 minutes (the host unhid it) lands on the wall without the "Just pinned" reveal.
- **`frontend/index.html`**: removed `maximum-scale=1.0, user-scalable=no` from the viewport meta so phones can pinch-zoom (accessibility).

### Verification Results
- `cd backend && go vet ./... && go test ./...`: all 7 tests pass (3 existing + 4 new admin tests). `gofmt` is clean.
- `cd frontend && npm run check`: **0 errors, 0 warnings**. `npm run build` succeeded.
- **End-to-end against the real Go handlers + SQLite.** A throwaway harness replaced MinIO with the in-memory object store, because the MinIO image could not be pulled locally. The wall was open in a second tab throughout:
  - Seeded 8 photos from 3 phones; the 6th upload from a capped phone returned `429`.
  - **Login:**
    - wrong PIN → "That PIN is not right."
    - correct PIN → panel; the session survives a reload
    - after logout → `/api/admin/session` returns `401`
    - 5 wrong PINs → "Try again in 60s" with the field disabled
  - **Settings:**
    - code `bad code` → field error
    - title change → the wall's `h1` updates live
    - code `GRAD-7` → the wall link becomes `/wall?code=GRAD-7`; public `/api/config` shows `requiresEventCode: true` without the code
    - clearing the code → "anyone with the link can upload now"
  - **Hide / unhide:** hide → wall cards 8 → 7, the Hidden filter shows it, and Spotlight is disabled; unhide → wall back to 8.
  - **Spotlight:** the wall shows "Host's pick" even while highlights are paused.
  - **Delete:** the dialog opens, Escape cancels, confirm removes the card; wall cards 8 → 7.
  - **Reset limit:** the dialog opens; after confirming, the card reads "Phone limit was reset", the capped phone's next upload returns `201`, and the new photo appears in the panel live.
  - **Album:** downloaded `graduation-album-2026-11-09.zip` with 8 photos (`001_andi_….jpg` … `008_…`), `memories.json`, and `index.html`. The unzipped `index.html` opened from `file://` showed 8 Polaroids with 0 broken images.
  - **Mobile (390px):** no horizontal overflow.
  - **Errors:** no page errors. The console showed only the expected HTTP statuses (session check `401`, wrong PINs, the `400` validation, the `429` lockout).
