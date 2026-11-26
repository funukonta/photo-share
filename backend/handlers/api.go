package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/photo-share/backend/config"
	"github.com/photo-share/backend/storage"
)

const (
	MaxUploadSizeBytes = 15 << 20 // 15 MB max upload size
	MaxNicknameRunes   = 40
	MaxCaptionRunes    = 100
)

var allowedReactionEmojis = map[string]bool{
	"🎉":  true,
	"🔥":  true,
	"🥹":  true,
	"❤️": true,
	"🚀":  true,
	"🐛":  true,
	"👏":  true,
	"✨":  true,
}

// API bundles dependencies for all HTTP endpoints.
type API struct {
	Cfg      *config.Config
	DB       *storage.SQLiteStore
	Objects  storage.PhotoObjectStore
	Hub      *SSEHub
	Identity *IdentityManager
	Host     *HostAuth
}

// NewAPI constructs an API handler suite.
func NewAPI(cfg *config.Config, db *storage.SQLiteStore, objects storage.PhotoObjectStore, hub *SSEHub) *API {
	return &API{
		Cfg:      cfg,
		DB:       db,
		Objects:  objects,
		Hub:      hub,
		Identity: NewIdentityManager(cfg.DeviceSecret),
		Host:     NewHostAuth(cfg.DeviceSecret, cfg.HostPIN),
	}
}

// RegisterRoutes attaches all API endpoints. Every guest/wall endpoint is scoped to a room: /api/rooms/{room}/...
func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("GET /api/rooms/{room}/config", a.withRoom(a.handleGetConfig))
	mux.HandleFunc("GET /api/rooms/{room}/me", a.withRoom(a.handleGetMe))
	mux.HandleFunc("GET /api/rooms/{room}/photos", a.withRoom(a.handleListPhotos))
	mux.HandleFunc("POST /api/rooms/{room}/photos", a.withRoom(a.handleUploadPhoto))
	mux.HandleFunc("POST /api/rooms/{room}/reactions", a.withRoom(a.handlePostReaction))
	mux.HandleFunc("GET /api/rooms/{room}/events", a.withRoom(a.handleRoomEvents))
	mux.HandleFunc("GET /api/photos/{id}/image", a.handleGetPhotoImage)
	a.registerAdminRoutes(mux)
}

type roomHandler func(w http.ResponseWriter, r *http.Request, ev storage.Event)

// withRoom resolves {room} to an event, answering 404 ROOM_NOT_FOUND for unknown or malformed codes.
func (a *API) withRoom(next roomHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ev, ok := a.loadEvent(w, r.PathValue("room"))
		if !ok {
			return
		}
		next(w, r, ev)
	}
}

func (a *API) loadEvent(w http.ResponseWriter, rawRoom string) (storage.Event, bool) {
	room, valid := storage.NormalizeRoomCode(rawRoom)
	if !valid {
		writeRoomNotFound(w)
		return storage.Event{}, false
	}
	ev, err := a.DB.GetEvent(room)
	if err != nil {
		if errors.Is(err, storage.ErrEventNotFound) {
			writeRoomNotFound(w)
			return storage.Event{}, false
		}
		writeError(w, http.StatusInternalServerError, "Failed to load event")
		return storage.Event{}, false
	}
	return ev, true
}

func writeRoomNotFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{
		"error": "No event with that room code. Check the code on the big screen.",
		"code":  "ROOM_NOT_FOUND",
	})
}

// PublicConfigResponse is the guest-facing view of an event. The event code itself is never included.
type PublicConfigResponse struct {
	Room               string `json:"room"`
	Title              string `json:"title"`
	Subtitle           string `json:"subtitle"`
	MaxPhotosPerPerson int    `json:"maxPhotosPerPerson"`
	RequiresEventCode  bool   `json:"requiresEventCode"`
	UploadsOpen        bool   `json:"uploadsOpen"`
}

func publicConfigFrom(ev storage.Event) PublicConfigResponse {
	return PublicConfigResponse{
		Room:               ev.ID,
		Title:              ev.Title,
		Subtitle:           ev.Subtitle,
		MaxPhotosPerPerson: ev.MaxPhotosPerPerson,
		RequiresEventCode:  strings.TrimSpace(ev.EventCode) != "",
		UploadsOpen:        ev.UploadsOpen,
	}
}

// MeResponse represents the device's quota state and uploaded photos in one event.
type MeResponse struct {
	DeviceID           string          `json:"deviceId"`
	DeviceToken        string          `json:"deviceToken"`
	UploadedCount      int             `json:"uploadedCount"`
	RemainingCount     int             `json:"remainingCount"`
	MaxPhotosPerPerson int             `json:"maxPhotosPerPerson"`
	Photos             []storage.Photo `json:"photos"`
}

func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func (a *API) handleGetConfig(w http.ResponseWriter, r *http.Request, ev storage.Event) {
	// Ensure device cookie is initialized early when user visits the page
	_, _ = a.Identity.ResolveOrIssueDevice(w, r)
	writeJSON(w, http.StatusOK, publicConfigFrom(ev))
}

func (a *API) handleGetMe(w http.ResponseWriter, r *http.Request, ev storage.Event) {
	deviceID, token := a.Identity.ResolveOrIssueDevice(w, r)

	photos, err := a.DB.ListPhotosByDevice(ev.ID, deviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load device photos")
		return
	}

	uploaded := len(photos)
	writeJSON(w, http.StatusOK, MeResponse{
		DeviceID:           deviceID,
		DeviceToken:        token,
		UploadedCount:      uploaded,
		RemainingCount:     max(0, ev.MaxPhotosPerPerson-uploaded),
		MaxPhotosPerPerson: ev.MaxPhotosPerPerson,
		Photos:             photos,
	})
}

func (a *API) handleListPhotos(w http.ResponseWriter, _ *http.Request, ev storage.Event) {
	photos, err := a.DB.ListVisiblePhotos(ev.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list photos")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"photos": photos,
		"total":  len(photos),
	})
}

func (a *API) handleRoomEvents(w http.ResponseWriter, r *http.Request, ev storage.Event) {
	a.Hub.ServeRoom(w, r, ev.ID)
}

func (a *API) handleUploadPhoto(w http.ResponseWriter, r *http.Request, ev storage.Event) {
	deviceID, token := a.Identity.ResolveOrIssueDevice(w, r)
	clientIP := ExtractClientIP(r)

	if !ev.UploadsOpen {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "Uploads are closed for this event.",
			"code":  "UPLOADS_CLOSED",
		})
		return
	}

	// Enforce maximum request body size
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadSizeBytes)
	if err := r.ParseMultipartForm(MaxUploadSizeBytes); err != nil {
		writeError(w, http.StatusBadRequest, "Photo file is too large (max 15MB) or malformed multipart form")
		return
	}

	// Optional event code set by the host for this event
	if requiredCode := strings.TrimSpace(ev.EventCode); requiredCode != "" {
		providedCode := strings.TrimSpace(r.FormValue("event_code"))
		if providedCode == "" {
			providedCode = strings.TrimSpace(r.URL.Query().Get("code"))
		}
		if providedCode == "" {
			providedCode = strings.TrimSpace(r.Header.Get("X-Event-Code"))
		}
		if !strings.EqualFold(providedCode, requiredCode) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "Invalid or missing event code. Please scan the QR code on the big screen.",
				"code":  "EVENT_CODE_INVALID",
			})
			return
		}
	}

	// 1. Per-device quota, counted per event
	deviceCount, err := a.DB.CountByDeviceID(ev.ID, deviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to verify device upload quota")
		return
	}
	if deviceCount >= ev.MaxPhotosPerPerson {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":              fmt.Sprintf("You have reached your limit of %d photos per person.", ev.MaxPhotosPerPerson),
			"code":               "DEVICE_QUOTA_EXCEEDED",
			"uploadedCount":      deviceCount,
			"remainingCount":     0,
			"maxPhotosPerPerson": ev.MaxPhotosPerPerson,
		})
		return
	}

	// 2. Shared-Wi-Fi safety ceiling, counted per event
	if ev.MaxPhotosPerIP > 0 && clientIP != "" {
		ipCount, err := a.DB.CountByIPAddress(ev.ID, clientIP)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to verify network upload limit")
			return
		}
		if ipCount >= ev.MaxPhotosPerIP {
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error": "Venue network upload safety limit reached. Please ask the host to increase the limit.",
				"code":  "IP_QUOTA_EXCEEDED",
			})
			return
		}
	}

	// 3. Validate nickname and caption
	nickname := strings.TrimSpace(r.FormValue("nickname"))
	if nickname == "" {
		nickname = "Anonymous Grad"
	}
	if utf8.RuneCountInString(nickname) > MaxNicknameRunes {
		nickname = truncateRunes(nickname, MaxNicknameRunes)
	}

	caption := strings.TrimSpace(r.FormValue("caption"))
	if utf8.RuneCountInString(caption) > MaxCaptionRunes {
		caption = truncateRunes(caption, MaxCaptionRunes)
	}

	// 4. Read and validate photo file
	file, fileHeader, err := r.FormFile("photo")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Missing 'photo' file field")
		return
	}
	defer file.Close()

	imgBytes, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read uploaded photo")
		return
	}
	if len(imgBytes) == 0 {
		writeError(w, http.StatusBadRequest, "Uploaded photo file is empty")
		return
	}

	detectedType := http.DetectContentType(imgBytes)
	ext, ok := imageExtensionForMIME(detectedType, fileHeader.Filename)
	if !ok {
		writeError(w, http.StatusBadRequest, "Unsupported image format. Please upload a JPEG, PNG, WebP, or GIF image.")
		return
	}

	photoID := newUUIDv4()
	// Files are grouped per event so an event's photos can be found (or removed) together on disk.
	objectKey := fmt.Sprintf("%s/%s%s", ev.ID, photoID, ext)

	if err := a.Objects.UploadPhoto(r.Context(), objectKey, bytes.NewReader(imgBytes), int64(len(imgBytes)), detectedType); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to store photo in object storage")
		return
	}

	photo, err := a.DB.InsertPhoto(storage.Photo{
		ID:          photoID,
		EventID:     ev.ID,
		ObjectKey:   objectKey,
		ContentType: detectedType,
		Nickname:    nickname,
		Caption:     caption,
		DeviceID:    deviceID,
		IPAddress:   clientIP,
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		_ = a.Objects.DeletePhoto(r.Context(), objectKey)
		writeError(w, http.StatusInternalServerError, "Failed to save photo metadata")
		return
	}

	a.Hub.Broadcast(ev.ID, EventPhotoAdded, publicPhoto(photo))

	newUploadedCount := deviceCount + 1
	writeJSON(w, http.StatusCreated, map[string]any{
		"photo":              photo,
		"deviceToken":        token,
		"uploadedCount":      newUploadedCount,
		"remainingCount":     max(0, ev.MaxPhotosPerPerson-newUploadedCount),
		"maxPhotosPerPerson": ev.MaxPhotosPerPerson,
	})
}

func (a *API) handleGetPhotoImage(w http.ResponseWriter, r *http.Request) {
	photoID := strings.TrimSpace(r.PathValue("id"))
	if photoID == "" {
		writeError(w, http.StatusBadRequest, "Missing photo ID")
		return
	}

	photo, err := a.DB.GetPhotoByID(photoID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Photo not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to lookup photo")
		return
	}

	reader, contentType, err := a.Objects.GetPhoto(r.Context(), photo.ObjectKey)
	if err != nil {
		writeError(w, http.StatusNotFound, "Photo image not found in storage")
		return
	}
	defer reader.Close()

	if photo.ContentType != "" {
		contentType = photo.ContentType
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

type reactionRequest struct {
	Emoji    string `json:"emoji"`
	Nickname string `json:"nickname"`
}

func (a *API) handlePostReaction(w http.ResponseWriter, r *http.Request, ev storage.Event) {
	var req reactionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	emoji := strings.TrimSpace(req.Emoji)
	if !allowedReactionEmojis[emoji] {
		writeError(w, http.StatusBadRequest, "Unsupported reaction emoji")
		return
	}

	nickname := strings.TrimSpace(req.Nickname)
	if utf8.RuneCountInString(nickname) > MaxNicknameRunes {
		nickname = truncateRunes(nickname, MaxNicknameRunes)
	}

	reactionPayload := map[string]any{
		"id":        newUUIDv4(),
		"emoji":     emoji,
		"nickname":  nickname,
		"createdAt": time.Now().UTC().Format(time.RFC3339Nano),
	}

	a.Hub.Broadcast(ev.ID, EventReaction, reactionPayload)
	writeJSON(w, http.StatusOK, reactionPayload)
}

func imageExtensionForMIME(mimeType, filename string) (string, bool) {
	switch mimeType {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "image/webp":
		return ".webp", true
	case "image/gif":
		return ".gif", true
	}
	// Fallback check on filename extension if browser uploaded HEIC/WebP that sniffed as octet-stream
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return ".jpg", true
	case ".png":
		return ".png", true
	case ".webp":
		return ".webp", true
	case ".gif":
		return ".gif", true
	}
	return "", false
}

func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{
		"error": message,
	})
}
