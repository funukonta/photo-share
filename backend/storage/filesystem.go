package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// FSObjectStore implements PhotoObjectStore on the local filesystem.
// It is the default on a Sprite, whose disk is already durable (NVMe cache backed by object storage),
// so running MinIO there would only add a second process to keep alive.
type FSObjectStore struct {
	root string
}

// NewFSObjectStore creates the photo directory if needed and returns a store rooted there.
func NewFSObjectStore(root string) (*FSObjectStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("photo directory is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve photo directory %q: %w", root, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create photo directory %q: %w", abs, err)
	}
	return &FSObjectStore{root: abs}, nil
}

// Root returns the absolute directory photos are written to.
func (s *FSObjectStore) Root() string { return s.root }

// pathFor maps an object key like "2026-11-25/<uuid>.jpg" to a file inside root, rejecting traversal.
func (s *FSObjectStore) pathFor(objectKey string) (string, error) {
	clean := path.Clean("/" + strings.ReplaceAll(objectKey, "\\", "/"))
	if clean == "/" || strings.Contains(objectKey, "..") {
		return "", fmt.Errorf("invalid object key %q", objectKey)
	}
	full := filepath.Join(s.root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	if !strings.HasPrefix(full, s.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid object key %q", objectKey)
	}
	return full, nil
}

// UploadPhoto writes to a temp file in the same directory, fsyncs, then renames,
// so a crash or pause mid-write never leaves a half-written photo under the final name.
func (s *FSObjectStore) UploadPhoto(_ context.Context, objectKey string, reader io.Reader, _ int64, _ string) error {
	dst, err := s.pathFor(objectKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create photo folder: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".upload-*")
	if err != nil {
		return fmt.Errorf("create temp photo: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := io.Copy(tmp, reader); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write photo: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync photo: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close photo: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("chmod photo: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		cleanup()
		return fmt.Errorf("store photo: %w", err)
	}
	return nil
}

// GetPhoto opens the stored file. The content type is guessed from the extension;
// the API prefers the type recorded in SQLite at upload time.
func (s *FSObjectStore) GetPhoto(_ context.Context, objectKey string) (io.ReadCloser, string, error) {
	p, err := s.pathFor(objectKey)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, "", fmt.Errorf("open photo %q: %w", objectKey, err)
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(p)))
	if contentType == "" {
		contentType = "image/jpeg"
	}
	return f, contentType, nil
}

// DeletePhoto removes the file; deleting a photo that is already gone is not an error.
func (s *FSObjectStore) DeletePhoto(_ context.Context, objectKey string) error {
	p, err := s.pathFor(objectKey)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete photo %q: %w", objectKey, err)
	}
	// Drop the event's folder once its last photo is gone (os.Remove fails harmlessly if it isn't empty).
	if dir := filepath.Dir(p); dir != s.root {
		_ = os.Remove(dir)
	}
	return nil
}
