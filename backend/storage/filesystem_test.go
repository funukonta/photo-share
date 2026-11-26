package storage_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/photo-share/backend/storage"
)

func TestFSObjectStoreRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "photos")
	s, err := storage.NewFSObjectStore(root)
	if err != nil {
		t.Fatalf("NewFSObjectStore: %v", err)
	}
	ctx := context.Background()
	data := []byte("\xff\xd8\xff fake jpeg bytes")

	if err := s.UploadPhoto(ctx, "2026-11-25/abc.jpg", bytes.NewReader(data), int64(len(data)), "image/jpeg"); err != nil {
		t.Fatalf("UploadPhoto: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "2026-11-25", "abc.jpg")); err != nil {
		t.Fatalf("expected file on disk: %v", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(root, "2026-11-25", ".upload-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}

	rc, ct, err := s.GetPhoto(ctx, "2026-11-25/abc.jpg")
	if err != nil {
		t.Fatalf("GetPhoto: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, data) || ct != "image/jpeg" {
		t.Fatalf("round trip mismatch: %q %q", got, ct)
	}

	if err := s.DeletePhoto(ctx, "2026-11-25/abc.jpg"); err != nil {
		t.Fatalf("DeletePhoto: %v", err)
	}
	if err := s.DeletePhoto(ctx, "2026-11-25/abc.jpg"); err != nil {
		t.Fatalf("second DeletePhoto should be a no-op, got %v", err)
	}
	if _, _, err := s.GetPhoto(ctx, "2026-11-25/abc.jpg"); err == nil {
		t.Fatalf("expected error reading deleted photo")
	}
}

func TestFSObjectStoreRejectsTraversal(t *testing.T) {
	s, err := storage.NewFSObjectStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFSObjectStore: %v", err)
	}
	for _, key := range []string{"../escape.jpg", "a/../../escape.jpg", "", "/"} {
		if err := s.UploadPhoto(context.Background(), key, bytes.NewReader([]byte("x")), 1, ""); err == nil {
			t.Fatalf("expected key %q to be rejected", key)
		}
	}
}
