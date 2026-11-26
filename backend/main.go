package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/photo-share/backend/config"
	"github.com/photo-share/backend/handlers"
	"github.com/photo-share/backend/storage"
)

func main() {
	cfg := config.Load()

	db, err := storage.NewSQLiteStore(cfg)
	if err != nil {
		log.Fatalf("failed to initialize sqlite store: %v", err)
	}
	defer func() { _ = db.Close() }()

	objects := openObjectStore(cfg)

	if cfg.ClientIPHeader != "" {
		log.Printf("trusting client IP header %s", cfg.ClientIPHeader)
	}
	handlers.SetTrustedClientIPHeader(cfg.ClientIPHeader)
	if strings.TrimSpace(cfg.HostPIN) == "" {
		log.Printf("HOST_PIN is empty: the /admin panel is disabled")
	} else if cfg.HostPIN == "2026" {
		log.Printf("WARNING: HOST_PIN is still the default; set a real one before the event")
	}

	hub := handlers.NewSSEHub()
	api := handlers.NewAPI(cfg, db, objects, hub)

	mux := http.NewServeMux()
	api.RegisterRoutes(mux)
	mux.Handle("/", staticHandler(cfg.StaticDir))

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           withCORS(mux, os.Getenv("CORS_ORIGINS")),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: /api/events is a long-lived SSE stream and the ZIP export can take minutes.
	}

	// Shut down cleanly on SIGTERM/SIGINT (service restarts, Docker stop) so SQLite closes its WAL.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		log.Printf("shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx) // SSE streams never finish on their own; the timeout cuts them off
	}()

	log.Printf("photo-share backend listening on %s (storage=%s, sqlite=%s)", srv.Addr, cfg.StorageBackend, cfg.SQLitePath)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}

func openObjectStore(cfg *config.Config) storage.PhotoObjectStore {
	switch cfg.StorageBackend {
	case "fs", "filesystem", "disk":
		fsStore, err := storage.NewFSObjectStore(cfg.PhotoDir)
		if err != nil {
			log.Fatalf("failed to prepare photo directory: %v", err)
		}
		log.Printf("storing photos on local disk at %s", fsStore.Root())
		return fsStore
	case "minio", "":
		var (
			minioStore *storage.MinioStore
			err        error
		)
		for attempt := 1; attempt <= 15; attempt++ {
			minioStore, err = storage.NewMinioStore(cfg)
			if err == nil {
				break
			}
			log.Printf("waiting for MinIO at %s (attempt %d/15): %v", cfg.MinioEndpoint, attempt, err)
			time.Sleep(2 * time.Second)
		}
		if err != nil {
			log.Fatalf("failed to connect to MinIO after retries: %v", err)
		}
		log.Printf("storing photos in MinIO bucket %q at %s", cfg.MinioBucket, cfg.MinioEndpoint)
		return minioStore
	default:
		log.Fatalf("unknown STORAGE_BACKEND %q (use \"fs\" or \"minio\")", cfg.StorageBackend)
		return nil
	}
}

// staticHandler serves the built Svelte SPA, falling back to index.html for client-side routes (/wall, /admin).
func staticHandler(staticDir string) http.Handler {
	info, err := os.Stat(staticDir)
	if err != nil || !info.IsDir() {
		log.Printf("no frontend build at %s; serving API only", staticDir)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"service":"photo-share-backend","status":"running"}`))
		})
	}

	log.Printf("serving static frontend from %s", staticDir)
	fileServer := http.FileServer(http.Dir(staticDir))
	index := filepath.Join(staticDir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		cleanPath := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		fullPath := filepath.Join(staticDir, cleanPath)
		if st, err := os.Stat(fullPath); err == nil && !st.IsDir() {
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				// Vite fingerprints asset names, so phones on venue Wi-Fi can cache them for good.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, index)
	})
}

// withCORS is opt-in. In production the SPA and API share one origin, so no CORS headers are needed;
// set CORS_ORIGINS (comma-separated) only when serving the frontend from somewhere else.
func withCORS(next http.Handler, allowed string) http.Handler {
	origins := map[string]bool{}
	for _, o := range strings.Split(allowed, ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins[o] = true
		}
	}
	if len(origins) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Device-Token, X-Host-Pin, X-Event-Code")
			w.Header().Set("Access-Control-Expose-Headers", "X-Device-Token")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
