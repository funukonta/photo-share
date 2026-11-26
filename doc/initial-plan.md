# Initial Plan — Graduation Live Photo Wall (`photo-share`)

- **Target Event Date:** November 25
- **Created Date:** November 8
- **Deployment Target:** Fly.io Sprite running the Go binary as a Sprite service, with photos on the Sprite's disk (see [`doc/step5.md`](./step5.md)). Docker Compose (`Go + Svelte + MinIO`) remains as a VPS alternative.

---

## 1. Vision & User Experience

A real-time, zero-friction collaborative photo wall designed for the final day of a graduation event:

1. **The Big Screen (`/wall`)**:
   - Displayed on the host laptop / projector.
   - Configurable **Event Title**, **Subtitle**, and **Theme Accent** (changeable via `.env` and live via the Host Admin panel).
   - **Polaroid Corkboard / Stage Wall**: Photos appear as tilted Polaroid snapshots with the uploader's nickname and handwritten-style message.
   - **Live Spotlight Reveal**: New uploads dim the stage, fly into the center with a Polaroid "developing" transition, hold for ~5 seconds, and smoothly animate (`animate:flip` / spring) onto the wall.
   - **Idle Memory Highlight**: Periodically spotlights random memories when no new uploads are arriving.
   - **Live Floating Emoji Stream**: Real-time emoji reactions (`🎉`, `🔥`, `🥹`, `❤️`, `🚀`, `🐛`) drift up the screen.
   - **Persistent QR Code Card**: Displays a scannable QR code pointing to the mobile upload URL and a live memory counter.

2. **The Mobile Uploader (`/`)**:
   - No login or account creation required.
   - Shows remaining personal quota (`5 / 5 photos left`).
   - Client-side `<canvas>` compression converts large 12MP+ phone photos into fast-uploading JPEGs (~1600px max dimension, ~82% quality) before sending to the server.
   - Captures **Nickname** (persisted in `localStorage`) and **Short Caption** (up to 100 chars).
   - Displays the user's uploaded Polaroids and a live **Emoji Reaction Bar**.

3. **Host Controls & Post-Event Keepsake (`/admin`)**:
   - Protected by `HOST_PIN`.
   - Live configuration of Event Title & Subtitle (broadcast immediately to `/wall`).
   - Moderation: Hide/unhide or delete any photo (instantly removed from `/wall`), or reset a device's 5-photo quota.
   - **Graduation Keepsake Export (`/api/admin/export.zip`)**: One-click ZIP download containing all photos plus `memories.json` and a standalone offline HTML album.

---

## 2. Fair "5 Photos Per Person" Quota Strategy (Venue Wi-Fi Safe)

Because classmates at the venue may share a single Wi-Fi NAT public IP or mobile carrier CGNAT:
- **Primary Identity (`device_id`)**: HMAC-signed `HttpOnly` cookie (`ps_device`) paired with a client token so each phone browser gets an isolated **5-photo quota** (configurable via `MAX_PHOTOS_PER_PERSON=5`).
- **Secondary Safety Limits**:
  - Per-IP ceiling (`MAX_PHOTOS_PER_IP=150`) to allow ~30 classmates on the same Wi-Fi while blocking automated abuse.
  - Optional Event Access Token (`EVENT_CODE`) embedded into the QR code URL (`/?code=...`) so only people in the room scanning the QR code can upload.

---

## 3. Architecture & Tech Stack

- **Object Storage**: **MinIO** (Dockerized open-source S3-compatible storage).
  - Bucket auto-initialized on backend startup.
- **Backend**: **Go (1.22+)**
  - `minio-go/v7` for object storage upload/streaming/ZIP export.
  - **SQLite** (stored on a Docker volume) for photo metadata, quota tracking, and runtime event settings (`title`, `subtitle`, `event_code`).
  - **Server-Sent Events (SSE)** hub (`/api/events`) for pushing `photo_added`, `photo_deleted`, `reaction`, and `config_updated` events to `/wall`.
- **Frontend**: **Svelte 5 + Vite + Tailwind CSS**
  - Built-in `svelte/motion`, `svelte/transition`, and `animate:flip` for Polaroid physics and floating emoji reactions.
  - Bundled into static assets and served by the Go server in production.

---

## 4. Implementation Steps & Traceability (`doc/stepX.md`)

Each step has its own dedicated documentation file in `/doc` recording its **Objectives**, **Planned Tasks**, **Status**, and **Changelog of Every File/Code Change**:

- [`doc/step1.md`](./step1.md) — **Step 1: Infrastructure, Object Storage (MinIO), & Go Backend Core**
- [`doc/step2.md`](./step2.md) — **Step 2: Svelte + Vite + Tailwind Frontend Scaffold & Mobile Upload Experience (`/`)**
- [`doc/step3.md`](./step3.md) — **Step 3: Live Polaroid Wall, Spotlight Reveal, QR Overlay & Emoji Reactions (`/wall`)**
- [`doc/step4.md`](./step4.md) — **Step 4: Host Moderation Panel, Configurable Event Settings & ZIP Album Export (`/admin`)**
- [`doc/step5.md`](./step5.md) — **Step 5: Production Docker Packaging, VPS Deployment Guide & End-to-End Verification**
