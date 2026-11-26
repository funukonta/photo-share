package handlers_test

import (
	"net/http/httptest"
	"testing"

	"github.com/photo-share/backend/handlers"
)

func TestExtractClientIPTrustedHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.2:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.9")
	r.Header.Set("X-Real-IP", "203.0.113.9")

	handlers.SetTrustedClientIPHeader("")
	if got := handlers.ExtractClientIP(r); got != "1.2.3.4" {
		t.Fatalf("default should use first X-Forwarded-For entry, got %q", got)
	}

	handlers.SetTrustedClientIPHeader("X-Real-IP")
	defer handlers.SetTrustedClientIPHeader("")
	if got := handlers.ExtractClientIP(r); got != "203.0.113.9" {
		t.Fatalf("trusted header should win over spoofable X-Forwarded-For, got %q", got)
	}

	r.Header.Del("X-Real-IP")
	if got := handlers.ExtractClientIP(r); got != "1.2.3.4" {
		t.Fatalf("missing trusted header should fall back, got %q", got)
	}
}
