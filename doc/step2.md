# Step 2 — Svelte + Vite + Tailwind Frontend Scaffold & Mobile Upload Experience (`/`)

- **Status:** Completed
- **Related Plan:** [`doc/initial-plan.md`](./initial-plan.md)

## 1. Objectives
1. Scaffold **Svelte 5 + Vite + Tailwind CSS** inside `frontend/` with clean routing for `/` (Mobile Upload), `/wall` (Big Screen), and `/admin` (Host Panel).
2. Build client-side `<canvas>` image compression (`frontend/src/lib/compress.ts`) so high-res phone photos resize & compress to fast JPEGs (< 1MB) before upload.
3. Build the **Mobile Upload Page (`/`)**:
   - Displays configurable **Event Title** & **Subtitle**.
   - Live quota counter pill (`X / 5 photos remaining`).
   - Camera / photo library picker with instant Polaroid preview.
   - Nickname input (remembered in `localStorage`) + short caption input (max 100 chars).
   - Gallery of the user's own uploaded Polaroids.
   - Live **Emoji Reaction Bar** (`🎉`, `🔥`, `🥹`, `❤️`, `🚀`, `🐛`, `👏`, `✨`) with cooldown feedback.

## 2. Detailed Change Log

### Scaffolded Svelte 5 + Vite + Tailwind CSS v4 Project (`frontend/`)
- **`frontend/package.json`**:
  - Configured Svelte 5 (`^5.20.0`), Vite 6 (`^6.2.0`), Tailwind CSS v4 (`tailwindcss`, `@tailwindcss/vite`), `svelte-check`, TypeScript, `lucide-svelte`, and `qrcode`.
- **`frontend/vite.config.ts`**:
  - Configured `@tailwindcss/vite` and `@sveltejs/vite-plugin-svelte` plugins, development `/api` proxy to `http://localhost:8080`, and production build output to `frontend/dist`.
- **`frontend/svelte.config.js`**, **`frontend/tsconfig.json`**, & **`frontend/src/vite-env.d.ts`**:
  - Configured Svelte 5 TypeScript preprocessing and strict type-checking.
- **`frontend/index.html`** & **`frontend/src/app.css`**:
  - Loaded Google Fonts (`Caveat` & `Kalam` for handwritten Polaroid captions; `Plus Jakarta Sans` & `Space Grotesk` for crisp UI/display typography).
  - Defined warm analog darkroom theme (`#141210` background, `#faf6f0` cream Polaroid paper, warm amber/orange shutter accents) and custom animations (`animate-develop`, `animate-emoji-burst`).
- **`frontend/src/main.ts`** & **`frontend/src/App.svelte`**:
  - Mounted Svelte 5 root component with client-side path routing (`window.location.pathname`) across `/` (`Upload.svelte`), `/wall` (`Wall.svelte`), and `/admin` (`Admin.svelte`).

### Created Frontend Client Libraries (`frontend/src/lib/`)
- **`frontend/src/lib/api.ts`**:
  - Implemented typed API client matching Go backend contracts in `backend/handlers/api.go`:
    - `fetchConfig()` (`GET /api/config`)
    - `fetchMe()` (`GET /api/me`)
    - `fetchPhotos()` (`GET /api/photos`)
    - `uploadPhoto()` (`POST /api/photos` multipart upload)
    - `sendReaction()` (`POST /api/reactions`)
  - Persists `deviceToken` in `localStorage` (`ps_device_token`) and sends it via `X-Device-Token` header alongside `credentials: 'include'` so mobile Safari ITP / WebViews retain their 5-photo identity.
  - Persists `nickname` (`ps_nickname`) and optional `event_code` (`ps_event_code`, auto-extracted from `?code=...` query string or manual input).
- **`frontend/src/lib/compress.ts`**:
  - Implemented `compressImageFile()` using `<canvas>` and `createImageBitmap(..., { imageOrientation: 'from-image' })` (with `HTMLImageElement` fallback) to resize images up to `maxDimension = 1600` at JPEG `quality = 0.84`.
  - Preserves aspect ratio, fills transparent backgrounds with warm cream `#FAF6F0`, reports original vs. compressed byte sizes (`formatBytes`), and falls back gracefully to the original `File` if canvas decoding fails.
- **`frontend/src/lib/sse.ts`**:
  - Implemented `connectSSE()` wrapper around `EventSource('/api/events')` with exponential backoff auto-reconnect and typed listeners for `photo_added`, `photo_deleted`, `photo_spotlight`, `reaction`, and `config_updated`.

### Implemented Mobile Upload Experience & Route Views (`frontend/src/routes/`)
- **`frontend/src/routes/Upload.svelte`**:
  - Built the complete Mobile Uploader (`/`):
    - Displays live configurable **Event Title** and **Subtitle** from `/api/config` and updates automatically on `config_updated` SSE events.
    - **Personal Film Roll Quota Badge**: Shows `X of 5 photos remaining` with visual film exposure dots, and transforms into a celebratory state when all 5 memories have been shared.
    - **Optional Event Code Prompt**: Auto-reads `?code=...` from the QR URL or prompts the user when `requiresEventCode` is enabled.
    - **Polaroid Composer Card**: Native camera/gallery picker (`accept="image/*"`), instant square Polaroid preview with developing animation, compression savings badge (`Compressed X MB → Y KB`), live handwritten caption preview (`font-handwriting`), `@nickname` tag, and character counter (`100` max).
    - **Live Emoji Reaction Bar**: 8-emoji bar (`🎉`, `🔥`, `🥹`, `❤️`, `🚀`, `🐛`, `👏`, `✨`) with local floating burst micro-animation and cooldown.
    - **"Your Shared Memories" Polaroid Strip**: 2-column tilted Polaroid gallery of the user's own uploaded photos with tap-to-enlarge lightbox modal and live SSE sync if a photo is moderated.
- **`frontend/src/routes/Wall.svelte`** & **`frontend/src/routes/Admin.svelte`**:
  - Created initial connected views for `/wall` and `/admin` ready for Step 3 and Step 4 expansion.

### Verification Results
- `cd frontend && npm install` — installed dependencies cleanly.
- `cd frontend && npm run check` — `svelte-check` passed with **0 errors and 0 warnings**.
- `cd frontend && npm run build` — Vite production bundle succeeded cleanly (`dist/index.html`, `dist/assets/*.css`, `dist/assets/*.js`).
