package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/photo-share/backend/storage"
)

const (
	HostCookieName = "ps_host"
	HostPinHeader  = "X-Host-Pin"

	hostSessionTTL        = 12 * time.Hour
	pinMaxFailures        = 5
	pinFailureWindow      = 5 * time.Minute
	pinLockoutDuration    = time.Minute
	MaxTitleRunes         = 80
	MaxSubtitleRunes      = 160
	MaxEventCodeRunes     = 40
	MaxPhotosPerPersonCap = 50
)

// HostAuth verifies the host PIN, issues signed host session cookies, and throttles PIN guessing per client IP.
type HostAuth struct {
	secret []byte
	pin    string
	now    func() time.Time

	mu       sync.Mutex
	attempts map[string]*pinAttempts
}

type pinAttempts struct {
	failures    int
	firstFail   time.Time
	lockedUntil time.Time
}

// NewHostAuth builds a host authenticator. Sessions are signed with the device secret and bound to the PIN,
// so changing HOST_PIN invalidates every existing host session.
func NewHostAuth(secret, pin string) *HostAuth {
	if strings.TrimSpace(secret) == "" {
		secret = "default-graduation-device-hmac-secret"
	}
	return &HostAuth{
		secret:   []byte(secret),
		pin:      strings.TrimSpace(pin),
		now:      time.Now,
		attempts: make(map[string]*pinAttempts),
	}
}

func (h *HostAuth) pinMatches(candidate string) bool {
	if h.pin == "" {
		return false // an empty HOST_PIN disables the admin panel instead of leaving it open
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(candidate)), []byte(h.pin)) == 1
}

func (h *HostAuth) sign(expiry int64) string {
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte("host|" + strconv.FormatInt(expiry, 10) + "|" + h.pin))
	return hex.EncodeToString(mac.Sum(nil))
}

// IssueSession returns a "<expiryUnix>.<hmac>" host session token.
func (h *HostAuth) IssueSession() (string, time.Time) {
	expiresAt := h.now().Add(hostSessionTTL)
	exp := expiresAt.Unix()
	return strconv.FormatInt(exp, 10) + "." + h.sign(exp), expiresAt
}

// VerifySession checks the token signature and expiry.
func (h *HostAuth) VerifySession(token string) bool {
	if h.pin == "" {
		return false
	}
	expStr, sigHex, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || h.now().Unix() >= exp {
		return false
	}
	provided, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	expected, _ := hex.DecodeString(h.sign(exp))
	return hmac.Equal(provided, expected)
}

// lockedFor reports how long the IP must wait before trying another PIN.
func (h *HostAuth) lockedFor(ip string) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	a, ok := h.attempts[ip]
	if !ok {
		return 0
	}
	if wait := a.lockedUntil.Sub(h.now()); wait > 0 {
		return wait
	}
	return 0
}

func (h *HostAuth) recordFailure(ip string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	a, ok := h.attempts[ip]
	if !ok || now.Sub(a.firstFail) > pinFailureWindow {
		a = &pinAttempts{firstFail: now}
		h.attempts[ip] = a
	}
	a.failures++
	if a.failures >= pinMaxFailures {
		a.lockedUntil = now.Add(pinLockoutDuration)
		a.failures = 0
		a.firstFail = now
	}
}

func (h *HostAuth) recordSuccess(ip string) {
	h.mu.Lock()
	delete(h.attempts, ip)
	h.mu.Unlock()
}

func (a *API) setHostCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     HostCookieName,
		Value:    value,
		Path:     "/api/admin",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteStrictMode,
	})
}

func writeLocked(w http.ResponseWriter, wait time.Duration) {
	secs := int(wait.Seconds() + 0.999)
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"error":      "Too many wrong PINs. Wait a minute and try again.",
		"code":       "PIN_LOCKED",
		"retryAfter": secs,
	})
}

// requireHost accepts either a valid host session cookie or the PIN in the X-Host-Pin header.
func (a *API) requireHost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(HostCookieName); err == nil && a.Host.VerifySession(c.Value) {
			next(w, r)
			return
		}
		if pin := r.Header.Get(HostPinHeader); pin != "" {
			ip := ExtractClientIP(r)
			if wait := a.Host.lockedFor(ip); wait > 0 {
				writeLocked(w, wait)
				return
			}
			if a.Host.pinMatches(pin) {
				a.Host.recordSuccess(ip)
				next(w, r)
				return
			}
			a.Host.recordFailure(ip)
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "Host PIN required",
			"code":  "HOST_AUTH_REQUIRED",
		})
	}
}

func (a *API) registerAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/login", a.handleAdminLogin)
	mux.HandleFunc("POST /api/admin/logout", a.handleAdminLogout)
	mux.HandleFunc("GET /api/admin/session", a.requireHost(a.handleAdminSession))

	// Events (rooms)
	mux.HandleFunc("GET /api/admin/events", a.requireHost(a.handleAdminListEvents))
	mux.HandleFunc("POST /api/admin/events", a.requireHost(a.handleAdminCreateEvent))
	mux.HandleFunc("GET /api/admin/events/{room}", a.requireHost(a.adminRoom(a.handleAdminGetEvent)))
	mux.HandleFunc("PUT /api/admin/events/{room}", a.requireHost(a.adminRoom(a.handleAdminUpdateEvent)))
	mux.HandleFunc("DELETE /api/admin/events/{room}", a.requireHost(a.adminRoom(a.handleAdminDeleteEvent)))
	mux.HandleFunc("GET /api/admin/events/{room}/photos", a.requireHost(a.adminRoom(a.handleAdminListPhotos)))
	mux.HandleFunc("POST /api/admin/events/{room}/devices/{deviceId}/reset", a.requireHost(a.adminRoom(a.handleAdminResetDevice)))
	mux.HandleFunc("GET /api/admin/events/{room}/export.zip", a.requireHost(a.adminRoom(a.handleAdminExport)))

	// Photos are addressed by ID; the broadcast goes to the photo's own room.
	mux.HandleFunc("PATCH /api/admin/photos/{id}", a.requireHost(a.handleAdminSetHidden))
	mux.HandleFunc("DELETE /api/admin/photos/{id}", a.requireHost(a.handleAdminDeletePhoto))
	mux.HandleFunc("POST /api/admin/photos/{id}/spotlight", a.requireHost(a.handleAdminSpotlight))
}

type adminLoginRequest struct {
	PIN string `json:"pin"`
}

func (a *API) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	ip := ExtractClientIP(r)
	if wait := a.Host.lockedFor(ip); wait > 0 {
		writeLocked(w, wait)
		return
	}

	var req adminLoginRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	if !a.Host.pinMatches(req.PIN) {
		a.Host.recordFailure(ip)
		if wait := a.Host.lockedFor(ip); wait > 0 {
			writeLocked(w, wait)
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "That PIN is not right.",
			"code":  "PIN_INVALID",
		})
		return
	}

	a.Host.recordSuccess(ip)
	token, expiresAt := a.Host.IssueSession()
	a.setHostCookie(w, r, token, int(hostSessionTTL.Seconds()))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"expiresAt": expiresAt.UTC().Format(time.RFC3339),
	})
}

func (a *API) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	a.setHostCookie(w, r, "", -1)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) handleAdminSession(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func publicPhoto(p storage.Photo) storage.Photo {
	p.DeviceID = ""
	p.Hidden = false
	return p
}

type adminHiddenRequest struct {
	Hidden *bool `json:"hidden"`
}

func (a *API) handleAdminSetHidden(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var req adminHiddenRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req); err != nil || req.Hidden == nil {
		writeError(w, http.StatusBadRequest, `Send {"hidden": true} or {"hidden": false}`)
		return
	}

	photo, err := a.DB.SetPhotoHidden(id, *req.Hidden)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Photo not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to update photo")
		return
	}

	if photo.Hidden {
		a.Hub.Broadcast(photo.EventID, EventPhotoDeleted, map[string]string{"id": photo.ID})
	} else {
		a.Hub.Broadcast(photo.EventID, EventPhotoAdded, publicPhoto(photo))
	}
	writeJSON(w, http.StatusOK, map[string]any{"photo": photo})
}

func (a *API) handleAdminDeletePhoto(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	photo, err := a.DB.DeletePhoto(id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Photo not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to delete photo")
		return
	}

	// The record is gone, so the photo is off every screen even if the object delete fails; log it for cleanup.
	if err := a.Objects.DeletePhoto(r.Context(), photo.ObjectKey); err != nil {
		log.Printf("admin delete: photo %s removed from db but object %q could not be deleted: %v", photo.ID, photo.ObjectKey, err)
	}

	a.Hub.Broadcast(photo.EventID, EventPhotoDeleted, map[string]string{"id": photo.ID})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": photo.ID})
}

func (a *API) handleAdminSpotlight(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	photo, err := a.DB.GetPhotoByID(id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Photo not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to load photo")
		return
	}
	if photo.Hidden {
		writeError(w, http.StatusConflict, "Unhide this photo before putting it in the spotlight")
		return
	}

	a.Hub.Broadcast(photo.EventID, EventPhotoSpotlight, publicPhoto(photo))
	writeJSON(w, http.StatusOK, map[string]any{"spotlighted": photo.ID})
}
