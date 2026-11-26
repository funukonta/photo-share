package handlers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
)

const (
	DeviceCookieName = "ps_device"
	DeviceHeaderName = "X-Device-Token"
)

// IdentityManager issues and verifies HMAC-SHA256 signed device tokens.
type IdentityManager struct {
	secret []byte
}

// NewIdentityManager initializes a device identity signer using the configured secret.
func NewIdentityManager(secret string) *IdentityManager {
	if strings.TrimSpace(secret) == "" {
		secret = "default-graduation-device-hmac-secret"
	}
	return &IdentityManager{
		secret: []byte(secret),
	}
}

// ResolveOrIssueDevice identifies the requesting device via signed cookie or X-Device-Token header.
// If neither is present or valid, it generates a fresh signed device token and sets the HttpOnly cookie.
// Returns (deviceID, signedToken).
func (im *IdentityManager) ResolveOrIssueDevice(w http.ResponseWriter, r *http.Request) (string, string) {
	// 1. Check cookie first
	if c, err := r.Cookie(DeviceCookieName); err == nil && c.Value != "" {
		if id, ok := im.VerifyToken(c.Value); ok {
			return id, c.Value
		}
	}

	// 2. Check X-Device-Token header fallback (useful for mobile WebViews or strict cookie contexts)
	if hdr := strings.TrimSpace(r.Header.Get(DeviceHeaderName)); hdr != "" {
		if id, ok := im.VerifyToken(hdr); ok {
			im.setCookie(w, hdr)
			return id, hdr
		}
	}

	// 3. Mint a new device ID + HMAC signature
	deviceID := newUUIDv4()
	token := im.SignDeviceID(deviceID)
	im.setCookie(w, token)
	w.Header().Set(DeviceHeaderName, token)
	return deviceID, token
}

// SignDeviceID returns "<deviceID>.<hex_hmac_sha256>".
func (im *IdentityManager) SignDeviceID(deviceID string) string {
	mac := hmac.New(sha256.New, im.secret)
	mac.Write([]byte(deviceID))
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%s.%s", deviceID, sig)
}

// VerifyToken checks if a "<deviceID>.<hex_hmac_sha256>" token has a valid signature.
func (im *IdentityManager) VerifyToken(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", false
	}
	deviceID, sigHex := parts[0], parts[1]
	if len(deviceID) < 8 || len(sigHex) == 0 {
		return "", false
	}

	expectedMac := hmac.New(sha256.New, im.secret)
	expectedMac.Write([]byte(deviceID))
	expectedSig := expectedMac.Sum(nil)

	providedSig, err := hex.DecodeString(sigHex)
	if err != nil {
		return "", false
	}

	if !hmac.Equal(expectedSig, providedSig) {
		return "", false
	}
	return deviceID, true
}

func (im *IdentityManager) setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     DeviceCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60, // 30 days
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// trustedClientIPHeader, when set, names a header written by a trusted reverse proxy that carries the
// real client IP. On Sprites the first X-Forwarded-For entry is whatever the client sent, so it cannot
// be trusted for the per-network upload limit or the PIN lockout; Sprite-Client-Ip can.
var trustedClientIPHeader string

// SetTrustedClientIPHeader configures ExtractClientIP. Call once at startup.
func SetTrustedClientIPHeader(header string) {
	trustedClientIPHeader = strings.TrimSpace(header)
}

// ExtractClientIP extracts the client IP from the trusted proxy header when configured,
// otherwise best-effort from X-Forwarded-For, X-Real-IP, or RemoteAddr.
func ExtractClientIP(r *http.Request) string {
	if trustedClientIPHeader != "" {
		if ip := strings.TrimSpace(r.Header.Get(trustedClientIPHeader)); ip != "" {
			return ip
		}
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}
	if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
		return xrip
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func newUUIDv4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
