package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/photo-share/backend/config"
)

// PhotoObjectStore abstracts photo binary storage (MinIO in production, in-memory for tests).
type PhotoObjectStore interface {
	UploadPhoto(ctx context.Context, objectKey string, reader io.Reader, size int64, contentType string) error
	GetPhoto(ctx context.Context, objectKey string) (io.ReadCloser, string, error)
	DeletePhoto(ctx context.Context, objectKey string) error
}

// MinioStore implements PhotoObjectStore backed by a MinIO / S3-compatible bucket.
type MinioStore struct {
	client *minio.Client
	bucket string
}

// NewMinioStore connects to MinIO and ensures the target bucket exists.
func NewMinioStore(cfg *config.Config) (*MinioStore, error) {
	client, err := minio.New(cfg.MinioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinioAccessKey, cfg.MinioSecretKey, ""),
		Secure: cfg.MinioUseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}

	store := &MinioStore{
		client: client,
		bucket: cfg.MinioBucket,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := store.EnsureBucket(ctx); err != nil {
		return nil, err
	}

	return store, nil
}

// EnsureBucket checks if the configured bucket exists and creates it if missing.
func (m *MinioStore) EnsureBucket(ctx context.Context) error {
	exists, err := m.client.BucketExists(ctx, m.bucket)
	if err != nil {
		return fmt.Errorf("check minio bucket %q: %w", m.bucket, err)
	}
	if !exists {
		if err := m.client.MakeBucket(ctx, m.bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("create minio bucket %q: %w", m.bucket, err)
		}
	}
	return nil
}

// UploadPhoto streams an image into the MinIO bucket.
func (m *MinioStore) UploadPhoto(ctx context.Context, objectKey string, reader io.Reader, size int64, contentType string) error {
	if contentType == "" {
		contentType = "image/jpeg"
	}
	_, err := m.client.PutObject(ctx, m.bucket, objectKey, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("put object %q: %w", objectKey, err)
	}
	return nil
}

// GetPhoto retrieves an object stream and its content type from MinIO.
func (m *MinioStore) GetPhoto(ctx context.Context, objectKey string) (io.ReadCloser, string, error) {
	obj, err := m.client.GetObject(ctx, m.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("get object %q: %w", objectKey, err)
	}
	info, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, "", fmt.Errorf("stat object %q: %w", objectKey, err)
	}
	contentType := info.ContentType
	if contentType == "" {
		contentType = "image/jpeg"
	}
	return obj, contentType, nil
}

// DeletePhoto removes an object from MinIO.
func (m *MinioStore) DeletePhoto(ctx context.Context, objectKey string) error {
	if err := m.client.RemoveObject(ctx, m.bucket, objectKey, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("remove object %q: %w", objectKey, err)
	}
	return nil
}

type memoryObject struct {
	data        []byte
	contentType string
}

// MemoryObjectStore is a thread-safe in-memory implementation of PhotoObjectStore for unit tests.
type MemoryObjectStore struct {
	mu      sync.RWMutex
	objects map[string]memoryObject
}

// NewMemoryObjectStore creates an empty in-memory object store.
func NewMemoryObjectStore() *MemoryObjectStore {
	return &MemoryObjectStore{
		objects: make(map[string]memoryObject),
	}
}

func (m *MemoryObjectStore) UploadPhoto(_ context.Context, objectKey string, reader io.Reader, _ int64, contentType string) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if contentType == "" {
		contentType = "image/jpeg"
	}
	m.mu.Lock()
	m.objects[objectKey] = memoryObject{
		data:        data,
		contentType: contentType,
	}
	m.mu.Unlock()
	return nil
}

func (m *MemoryObjectStore) GetPhoto(_ context.Context, objectKey string) (io.ReadCloser, string, error) {
	m.mu.RLock()
	obj, ok := m.objects[objectKey]
	m.mu.RUnlock()
	if !ok {
		return nil, "", fmt.Errorf("object %q not found", objectKey)
	}
	return io.NopCloser(bytes.NewReader(obj.data)), obj.contentType, nil
}

func (m *MemoryObjectStore) DeletePhoto(_ context.Context, objectKey string) error {
	m.mu.Lock()
	delete(m.objects, objectKey)
	m.mu.Unlock()
	return nil
}
