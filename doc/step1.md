# Step 1 — Infrastructure, Object Storage (MinIO), & Go Backend Core

- **Status:** Completed
- **Related Plan:** [`doc/initial-plan.md`](./initial-plan.md)

## 1. Objectives
1. Set up Dockerized **MinIO** object storage and configuration environment (`.env.example`, `docker-compose.yml`).
2. Initialize the **Go backend** (`backend/`) with automatic MinIO bucket creation (`photos`).
3. Implement **SQLite metadata & runtime config store** (`photos` table + `settings` table seeded from `.env` for configurable event title, subtitle, quota, and host PIN).
4. Implement **HMAC-signed Device Identity + IP safety middleware** enforcing the 5-photos-per-person rule safely on shared venue Wi-Fi.
5. Build core REST & real-time endpoints:
   - `GET /api/config` & `GET /api/me` (event config + personal remaining quota + uploaded photos)
   - `POST /api/photos` (multipart image upload -> MinIO + SQLite + SSE broadcast)
   - `GET /api/photos` & `GET /api/photos/:id/image` (list memories & stream photo from MinIO)
   - `POST /api/reactions` (broadcast live emoji reaction)
   - `GET /api/events` (SSE stream for live `/wall` updates)

## 2. Detailed Change Log

### Created Infrastructure & Environment Files
- **`.env.example`**:
  - Defined all configurable environment variables: `PORT`, `STATIC_DIR`, `MINIO_ENDPOINT`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_BUCKET`, `MINIO_USE_SSL`, `SQLITE_PATH`, `DEVICE_SECRET`, `HOST_PIN`, `EVENT_CODE`, `EVENT_TITLE`, `EVENT_SUBTITLE`, `MAX_PHOTOS_PER_PERSON`, and `MAX_PHOTOS_PER_IP`.
- **`docker-compose.yml`**:
  - Added `minio` service (`minio/minio:latest`) exposing API port `9000` and console port `9001`, backed by persistent `minio_data` volume and `mc ready local` healthcheck.
  - Added `app` service definition connected to `minio` and persistent `app_data` volume for SQLite storage.

### Created Go Backend (`backend/`)
- **`backend/go.mod` & `backend/go.sum`**:
  - Initialized Go module `github.com/photo-share/backend` with `github.com/minio/minio-go/v7` and pure-Go `modernc.org/sqlite` (zero CGO dependency).
- **`backend/config/config.go`**:
  - Implemented `Config` struct and `Load()` helper reading all environment variables with sensible defaults (`MAX_PHOTOS_PER_PERSON=5`, `MAX_PHOTOS_PER_IP=150`, configurable `EVENT_TITLE`, `EVENT_SUBTITLE`, `HOST_PIN`, etc.).
- **`backend/storage/minio.go`**:
  - Implemented `PhotoObjectStore` interface (`UploadPhoto`, `GetPhoto`, `DeletePhoto`).
  - Implemented `MinioStore` using `minio-go/v7` with automatic bucket existence check and creation (`EnsureBucket`).
  - Implemented thread-safe `MemoryObjectStore` for fast unit/integration testing without requiring a live MinIO daemon.
- **`backend/storage/sqlite.go`**:
  - Implemented `SQLiteStore` with WAL mode and schema migration for `photos` (`id`, `object_key`, `content_type`, `nickname`, `caption`, `device_id`, `ip_address`, `hidden`, `created_at`) and `settings` (`key`, `value`).
  - Seeded runtime event settings from `.env` defaults on first boot (`event_title`, `event_subtitle`, `event_code`, `max_photos_per_person`, `max_photos_per_ip`).
  - Added query and moderation helpers: `GetSettings`, `UpdateSettings`, `InsertPhoto`, `GetPhotoByID`, `ListVisiblePhotos`, `ListAllPhotos`, `ListPhotosByDevice`, `CountByDeviceID`, `CountByIPAddress`, `SetPhotoHidden`, `DeletePhoto`, and `ResetDeviceQuota`.
- **`backend/handlers/identity.go`**:
  - Implemented `IdentityManager` issuing and verifying `<uuid>.<hmac_sha256>` tokens via `ps_device` `HttpOnly` cookie and `X-Device-Token` header fallback so every phone browser gets an isolated quota even behind shared venue Wi-Fi NAT.
  - Added `ExtractClientIP` helper inspecting `X-Forwarded-For`, `X-Real-IP`, and `RemoteAddr`.
- **`backend/handlers/sse.go`**:
  - Implemented `SSEHub` managing concurrent SSE client subscriptions, 20-second keepalive comments, and real-time JSON broadcasts (`photo_added`, `photo_deleted`, `photo_spotlight`, `reaction`, `config_updated`).
- **`backend/handlers/api.go`**:
  - Implemented `GET /api/health`, `GET /api/config`, `GET /api/me`, `GET /api/photos`, `POST /api/photos`, `GET /api/photos/{id}/image`, `POST /api/reactions`, and `GET /api/events`.
  - Enforced 15MB max upload size, MIME sniffing (`image/jpeg`, `image/png`, `image/webp`, `image/gif`), optional `event_code` verification, per-device quota (`429 DEVICE_QUOTA_EXCEEDED`), and per-IP safety ceiling (`429 IP_QUOTA_EXCEEDED`).
- **`backend/main.go`**:
  - Wired configuration, SQLite initialization, MinIO startup retry loop (15 attempts), SSE hub, CORS middleware (with credentials & custom headers), and static SPA fallback serving from `STATIC_DIR`.
- **`backend/handlers/api_test.go`**:
  - Added automated integration tests covering `GET /api/config`, `GET /api/me` identity issuance, 5-photo-per-person enforcement (`429` on 6th upload), shared venue Wi-Fi IP isolation (second device on same IP still gets full quota), image streaming (`GET /api/photos/{id}/image`), `event_code` protection, and live SSE broadcasts for `photo_added` and `reaction`.

### Verification Results
- `cd backend && go build ./...` — succeeded cleanly with zero warnings or errors.
- `cd backend && go test -v ./...` — all tests passed (`TestConfigAndMeIdentityFlow`, `TestFivePhotoPerPersonQuotaAndSharedWiFiIsolation`, `TestEventCodeProtectionAndReactions`).
