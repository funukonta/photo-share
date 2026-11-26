# Step 3 — Live Polaroid Wall, Spotlight Reveal, QR Overlay & Emoji Reactions (`/wall`)

- **Status:** Completed
- **Related Plan:** [`doc/initial-plan.md`](./initial-plan.md)

## 1. Objectives
1. Build the **Big Screen Share View (`/wall`)** optimized for laptop/projector display (16:9 / fullscreen toggle).
2. Implement **Polaroid Corkboard / Stage Wall**:
   - Polaroid cards with deterministic slight tilt (`-4deg` to `+4deg`), handwritten-style caption font, sender nickname badge, and relative timestamp.
   - Smooth Svelte layout transitions (`animate:flip`, `scale`, `fly`).
3. Implement **Live Spotlight Reveal**:
   - When `photo_added` arrives over SSE, queue and spotlight the new Polaroid in the center of the screen with a "photo developing" animation before settling onto the wall.
   - Include an **Idle Memory Highlight** that periodically spotlights existing photos when no uploads have arrived recently.
4. Implement **Live Floating Emoji Stream**:
   - Listen to `reaction` SSE events and animate floating emojis rising along the screen.
5. Implement **Persistent QR Code Card**:
   - Auto-generates a scannable QR code pointing to the mobile upload URL + displays live total photo count.

## 2. Detailed Change Log

### New Wall Helpers (`frontend/src/lib/wall.ts`)
- `tiltFor(id)`: FNV-1a hash of the photo ID mapped to `-4deg..+4deg` (never flatter than `0.6deg`), so a card keeps the same tilt on every screen and reload.
- `relativeTime(iso, now)`: `just now`, `Xm ago`, `Xh ago`, `Xd ago`.
- `sortNewestFirst(photos)`.
- `computeWallLayout(width, height, count)`: picks the largest card width that fits every photo on the board **without scrolling**. Once the board is full, it falls back to the densest readable grid and returns `capacity`. Older photos beyond capacity stay in the idle highlight rotation.
- `buildUploadTarget(location)`: builds the QR target URL.
  - `/wall?code=XYZ` forwards the event code into the QR (`/?code=XYZ`).
  - `/wall?url=http://192.168.1.20:8080/` overrides the base URL when the projector laptop opens the wall via `localhost`.
  - Flags `pointsToLocalhost` so the wall can warn the host.

### New Components (`frontend/src/components/`)
- **`Polaroid.svelte`**: one card shared by the wall and the spotlight.
  - All sizes are in container-query units (`cqw`) on an outer `.frame` container. The card keeps the same proportions at 150px on a full wall and ~650px in the spotlight. Ratio is fixed at `1 : 1.23` (5cqw top border, 90cqw square photo, 28cqw caption strip).
  - Masking-tape strip at the top (the wall's identity motif), Caveat caption clamped to 2 lines, `@nickname`, `<time>` relative timestamp.
  - Square crop with `object-position: 50% 35%` so faces in portrait phone shots stay in frame.
  - Optional `developing` prop: 3.2s chemistry fade (dark sepia blur → overexposed → full colour). Disabled under `prefers-reduced-motion`.
- **`QrCard.svelte`**: the QR is printed on a taped Polaroid ("Scan to pin your photos").
  - SVG QR via `qrcode` (`errorCorrectionLevel: 'M'`, cream/ink colours), readable URL under it, live memory counter, and the per-person limit from `/api/config`.
  - Warnings (`role="alert"`) when the event requires a code but the wall URL has none, or when the QR points to `localhost`.
  - Loading ("Drawing QR code") and error ("type the address below") states.
- **`EmojiStream.svelte`**: fixed, `pointer-events: none`, `aria-hidden` layer with an exported `push(reaction)`.
  - Each emoji gets random x, sway, size, and a 4.2–6.0s rise. The sender's nickname is shown under it when present.
  - Capped at 48 nodes (oldest dropped). Nodes are removed on `animationend`.
  - Under `prefers-reduced-motion`, emojis fade in and out in place near the bottom edge without travelling.

### Rewritten Big Screen View (`frontend/src/routes/Wall.svelte`)
- **Layout:** full-viewport, no-scroll 16:9 stage on ≥768px. Title and subtitle on top, the board on the left, the QR rail on the right. Below 768px it becomes a scrolling 2-column wall with a "Pin your photos" link to `/`.
- **Wall:** keyed `{#each}` with `animate:flip` (700ms), `in:fade` for new cards, `out:scale` for deleted ones. If photos exist past capacity, the board shows "+N older photos in the highlight rotation".
- **Spotlight queue** (`new` / `memory` / `host` kinds):
  - `photo_added` reserves a slot at the front of the wall right away. Queued uploads show as blank "developing" Polaroids. The current one shows a dashed outline where it will land.
  - The stage dims the room, gives a single soft camera flash, and slides the print up from below the screen edge. The photo develops, holds for **5s** (3s when 3+ uploads are waiting; max queue 12, overflow lands directly on the wall), then flies onto its slot using `crossfade` (`send`/`receive` keyed by photo ID).
  - The stage shows "Just pinned", "Looking back" or "Host's pick", plus "N more photos developing".
  - `stageItem` is kept separately from `current` so the exit flight can still render after `current` is cleared.
- **Idle Memory Highlight:** after 20s with no uploads or spotlights, a random photo (avoiding the last 6 shown, including off-wall ones) flies from its card to the center for 6s and back.
- **Host spotlight:** `photo_spotlight` SSE events jump the queue (8s hold). Ready for Step 4's admin panel.
- **Moderation sync:** `photo_deleted` removes the card, drops it from the queue, and ends its spotlight if it is on stage.
- **Resilience:**
  - Loading, empty ("Nothing pinned yet"), and error (auto-retry every 5s + "Retry now") states.
  - Live / "Reconnecting to the live feed" status. After an SSE reconnect, the wall refetches photos and config to catch missed events.
- **Projector chrome:**
  - "Pause highlights" (`aria-pressed`, shortcut **P**) stops new-upload and idle spotlights. New photos then land directly on the wall.
  - "Fullscreen" (Fullscreen API, shortcut **F**).
  - Controls and the cursor auto-hide after 3s without mouse or keyboard input, and stay visible while a control has keyboard focus.
  - A visually hidden `aria-live` region announces new photos.
- **Reduced motion:** crossfade, flip, and fades drop to 0ms. No flash, no develop filter.

### Global Styles (`frontend/src/app.css`)
- Base `cursor: pointer` for enabled interactive elements and `cursor: not-allowed` for disabled buttons.

### Verification Results
- `cd frontend && npm run check`: **0 errors, 0 warnings**.
- `cd frontend && npm run build`: succeeded.
- Headless Chromium click-through against a mock of the API contract (`/api/config`, `/api/photos`, `/api/events` SSE, image endpoint). MinIO could not be pulled locally, so the Go backend was not run:
  - Initial load: 6 cards, QR SVG rendered, status "Live".
  - 1 upload: stage shows "Just pinned" with develop + flash, then clears after the hold, and the card lands at slot 1.
  - Burst of 4: "3 more photos developing", 3 undeveloped slots.
  - 12 reactions: 12 floating emojis, nicknames shown.
  - Pause button: stage closes, queue cleared, `aria-pressed=true`. A new upload while paused does not spotlight. **P** toggles back.
  - Delete event: card removed. `config_updated`: title changes live.
  - SSE dropped: status "Reconnecting to the live feed", then "Live" again with a refetch.
  - 20s idle: "Looking back" memory spotlight.
  - 72 photos at 1920×1080: 36 cards fit with no page scroll; the rest are counted as off-wall.
  - Controls and cursor hide after 3s idle, return on mouse move, and stay while focused via Tab.
  - States: empty, error + retry, event code missing warning, `?code=GRAD26&url=…` gives QR `192.168.1.20:8080/?code=GRAD26`.
  - Layouts: 1366×768 laptop (rail fits, no overflow), 390×844 mobile (no horizontal overflow), reduced-motion spotlight completes and clears.
  - No page errors. The only console error is the expected `ERR_INCOMPLETE_CHUNKED_ENCODING` from the deliberately killed SSE stream.
