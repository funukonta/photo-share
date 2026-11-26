package config

import (
	"os"
	"strconv"
	"strings"
)

// Config holds application startup and default event settings loaded from environment variables.
type Config struct {
	Port               string
	MinioEndpoint      string
	MinioAccessKey     string
	MinioSecretKey     string
	MinioBucket        string
	MinioUseSSL        bool
	SQLitePath         string
	DeviceSecret       string
	HostPIN            string
	EventTitle         string
	EventSubtitle      string
	EventCode          string
	MaxPhotosPerPerson int
	MaxPhotosPerIP     int
	StaticDir          string
	// StorageBackend selects where photo bytes live: "fs" (local disk, used on Sprites) or "minio".
	StorageBackend string
	// PhotoDir is the root folder for the "fs" storage backend.
	PhotoDir string
	// ClientIPHeader names a header set by a trusted proxy that carries the real client IP
	// ("Sprite-Client-Ip" on Sprites, "Fly-Client-IP" on Fly Machines). Empty means guess from
	// X-Forwarded-For / X-Real-IP / RemoteAddr.
	ClientIPHeader string
}

// Load reads environment variables and falls back to sensible local development defaults.
func Load() *Config {
	return &Config{
		Port:               getEnv("PORT", "8080"),
		MinioEndpoint:      getEnv("MINIO_ENDPOINT", "localhost:9000"),
		MinioAccessKey:     getEnv("MINIO_ACCESS_KEY", "minioadmin"),
		MinioSecretKey:     getEnv("MINIO_SECRET_KEY", "minioadmin123"),
		MinioBucket:        getEnv("MINIO_BUCKET", "photos"),
		MinioUseSSL:        getEnvBool("MINIO_USE_SSL", false),
		SQLitePath:         getEnv("SQLITE_PATH", "./data/photoshare.db"),
		DeviceSecret:       getEnv("DEVICE_SECRET", "dev-graduation-device-hmac-secret-2026"),
		HostPIN:            getEnv("HOST_PIN", "2026"),
		EventTitle:         getEnv("EVENT_TITLE", "Graduation Wall 🎓"),
		EventSubtitle:      getEnv("EVENT_SUBTITLE", "Scan the QR code to drop up to 5 favorite memories on the big screen!"),
		EventCode:          strings.TrimSpace(getEnv("EVENT_CODE", "")),
		MaxPhotosPerPerson: getEnvInt("MAX_PHOTOS_PER_PERSON", 5),
		MaxPhotosPerIP:     getEnvInt("MAX_PHOTOS_PER_IP", 150),
		StaticDir:          getEnv("STATIC_DIR", "../frontend/dist"),
		StorageBackend:     strings.ToLower(strings.TrimSpace(getEnv("STORAGE_BACKEND", "minio"))),
		PhotoDir:           getEnv("PHOTO_DIR", "./data/photos"),
		ClientIPHeader:     strings.TrimSpace(getEnv("CLIENT_IP_HEADER", "")),
	}
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func getEnvBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return b
}
