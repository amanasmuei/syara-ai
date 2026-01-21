// Package storage provides object storage operations.
package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ObjectStorage defines the interface for object storage operations.
type ObjectStorage interface {
	// Upload operations
	UploadFile(ctx context.Context, localPath, remotePath string) (string, error)
	UploadBytes(ctx context.Context, data []byte, path, contentType string) (string, error)
	UploadReader(ctx context.Context, reader io.Reader, size int64, path, contentType string) (string, error)

	// Download operations
	Download(ctx context.Context, path string) ([]byte, error)
	DownloadToWriter(ctx context.Context, path string, writer io.Writer) error
	GetURL(ctx context.Context, path string) (string, error)

	// Signed URLs for secure access
	GenerateSignedURL(ctx context.Context, path string, expiry time.Duration) (string, error)
	GenerateUploadURL(ctx context.Context, path string, expiry time.Duration) (string, error)

	// Management
	Delete(ctx context.Context, path string) error
	DeleteMultiple(ctx context.Context, paths []string) error
	Exists(ctx context.Context, path string) (bool, error)
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
	Copy(ctx context.Context, srcPath, dstPath string) error

	// Health check
	Health(ctx context.Context) error
}

// ObjectInfo represents metadata about a stored object.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	ContentType  string
	ETag         string
}

// MinIOConfig holds MinIO connection configuration.
type MinIOConfig struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	BucketName      string
	UseSSL          bool
	Region          string
}

// MinIOStorage implements ObjectStorage using MinIO SDK.
type MinIOStorage struct {
	client     *minio.Client
	bucketName string
	region     string
}

// Bucket paths for different content types.
const (
	PathOriginals   = "originals"
	PathPages       = "pages"
	PathThumbs      = "thumbs"
	PathHighlighted = "highlighted"
	PathCropped     = "cropped"
	PathGenerated   = "generated-pdfs"
)

// NewMinIOStorage creates a new MinIO storage client.
func NewMinIOStorage(cfg MinIOConfig) (*MinIOStorage, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	storage := &MinIOStorage{
		client:     client,
		bucketName: cfg.BucketName,
		region:     cfg.Region,
	}

	return storage, nil
}

// InitBucket ensures the bucket exists and creates it if necessary.
func (s *MinIOStorage) InitBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucketName)
	if err != nil {
		return fmt.Errorf("failed to check bucket existence: %w", err)
	}

	if !exists {
		err = s.client.MakeBucket(ctx, s.bucketName, minio.MakeBucketOptions{
			Region: s.region,
		})
		if err != nil {
			return fmt.Errorf("failed to create bucket: %w", err)
		}
	}

	return nil
}

// Health checks MinIO connectivity.
func (s *MinIOStorage) Health(ctx context.Context) error {
	_, err := s.client.BucketExists(ctx, s.bucketName)
	return err
}

// UploadFile uploads a file from local path to remote path.
func (s *MinIOStorage) UploadFile(ctx context.Context, localPath, remotePath string) (string, error) {
	contentType := detectContentType(localPath)

	info, err := s.client.FPutObject(ctx, s.bucketName, remotePath, localPath, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload file: %w", err)
	}

	return info.Key, nil
}

// UploadBytes uploads byte data to remote path.
func (s *MinIOStorage) UploadBytes(ctx context.Context, data []byte, path, contentType string) (string, error) {
	reader := bytes.NewReader(data)

	if contentType == "" {
		contentType = http.DetectContentType(data)
	}

	info, err := s.client.PutObject(ctx, s.bucketName, path, reader, int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload bytes: %w", err)
	}

	return info.Key, nil
}

// UploadReader uploads data from a reader to remote path.
func (s *MinIOStorage) UploadReader(ctx context.Context, reader io.Reader, size int64, path, contentType string) (string, error) {
	info, err := s.client.PutObject(ctx, s.bucketName, path, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload from reader: %w", err)
	}

	return info.Key, nil
}

// Download downloads an object and returns its contents.
func (s *MinIOStorage) Download(ctx context.Context, path string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucketName, path, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to read object: %w", err)
	}

	return data, nil
}

// DownloadToWriter downloads an object to a writer.
func (s *MinIOStorage) DownloadToWriter(ctx context.Context, path string, writer io.Writer) error {
	obj, err := s.client.GetObject(ctx, s.bucketName, path, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to get object: %w", err)
	}
	defer obj.Close()

	_, err = io.Copy(writer, obj)
	if err != nil {
		return fmt.Errorf("failed to copy object: %w", err)
	}

	return nil
}

// GetURL returns the direct URL to an object (for public buckets).
func (s *MinIOStorage) GetURL(ctx context.Context, path string) (string, error) {
	protocol := "http"
	if s.client.IsOnline() {
		// Check if using SSL
		endpoint := s.client.EndpointURL()
		if endpoint.Scheme == "https" {
			protocol = "https"
		}
	}

	return fmt.Sprintf("%s://%s/%s/%s", protocol, s.client.EndpointURL().Host, s.bucketName, path), nil
}

// GenerateSignedURL generates a presigned URL for downloading.
func (s *MinIOStorage) GenerateSignedURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	url, err := s.client.PresignedGetObject(ctx, s.bucketName, path, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("failed to generate signed URL: %w", err)
	}

	return url.String(), nil
}

// GenerateUploadURL generates a presigned URL for uploading.
func (s *MinIOStorage) GenerateUploadURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	url, err := s.client.PresignedPutObject(ctx, s.bucketName, path, expiry)
	if err != nil {
		return "", fmt.Errorf("failed to generate upload URL: %w", err)
	}

	return url.String(), nil
}

// Delete removes an object from storage.
func (s *MinIOStorage) Delete(ctx context.Context, path string) error {
	err := s.client.RemoveObject(ctx, s.bucketName, path, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}

	return nil
}

// DeleteMultiple removes multiple objects from storage.
func (s *MinIOStorage) DeleteMultiple(ctx context.Context, paths []string) error {
	objectsCh := make(chan minio.ObjectInfo)

	go func() {
		defer close(objectsCh)
		for _, path := range paths {
			objectsCh <- minio.ObjectInfo{Key: path}
		}
	}()

	for err := range s.client.RemoveObjects(ctx, s.bucketName, objectsCh, minio.RemoveObjectsOptions{}) {
		if err.Err != nil {
			return fmt.Errorf("failed to delete object %s: %w", err.ObjectName, err.Err)
		}
	}

	return nil
}

// Exists checks if an object exists.
func (s *MinIOStorage) Exists(ctx context.Context, path string) (bool, error) {
	_, err := s.client.StatObject(ctx, s.bucketName, path, minio.StatObjectOptions{})
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return false, nil
		}
		return false, fmt.Errorf("failed to check object existence: %w", err)
	}

	return true, nil
}

// List lists objects with the given prefix.
func (s *MinIOStorage) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	var objects []ObjectInfo

	objectCh := s.client.ListObjects(ctx, s.bucketName, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})

	for obj := range objectCh {
		if obj.Err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", obj.Err)
		}

		objects = append(objects, ObjectInfo{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
			ContentType:  obj.ContentType,
			ETag:         obj.ETag,
		})
	}

	return objects, nil
}

// Copy copies an object from source to destination.
func (s *MinIOStorage) Copy(ctx context.Context, srcPath, dstPath string) error {
	src := minio.CopySrcOptions{
		Bucket: s.bucketName,
		Object: srcPath,
	}

	dst := minio.CopyDestOptions{
		Bucket: s.bucketName,
		Object: dstPath,
	}

	_, err := s.client.CopyObject(ctx, dst, src)
	if err != nil {
		return fmt.Errorf("failed to copy object: %w", err)
	}

	return nil
}

// Helper functions

// detectContentType detects content type from file extension.
func detectContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))

	contentTypes := map[string]string{
		".pdf":  "application/pdf",
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".webp": "image/webp",
		".svg":  "image/svg+xml",
		".json": "application/json",
		".html": "text/html",
		".txt":  "text/plain",
		".css":  "text/css",
		".js":   "application/javascript",
	}

	if ct, ok := contentTypes[ext]; ok {
		return ct
	}

	return "application/octet-stream"
}

// BuildPath constructs a storage path for different content types.
func BuildPath(category, sourceType, docID, filename string) string {
	return filepath.Join(category, sourceType, docID, filename)
}

// BuildOriginalPath constructs a path for original documents.
func BuildOriginalPath(sourceType, filename string) string {
	return filepath.Join(PathOriginals, sourceType, filename)
}

// BuildPagePath constructs a path for page images.
func BuildPagePath(sourceType, docID string, pageNum int) string {
	return filepath.Join(PathPages, sourceType, docID, fmt.Sprintf("page-%d.png", pageNum))
}

// BuildThumbPath constructs a path for thumbnails.
func BuildThumbPath(sourceType, docID string, pageNum int) string {
	return filepath.Join(PathThumbs, sourceType, docID, fmt.Sprintf("page-%d-thumb.png", pageNum))
}

// BuildHighlightedPath constructs a path for highlighted images.
func BuildHighlightedPath(queryID, chunkID string) string {
	return filepath.Join(PathHighlighted, queryID, fmt.Sprintf("%s-highlighted.png", chunkID))
}

// BuildCroppedPath constructs a path for cropped images.
func BuildCroppedPath(queryID, chunkID string) string {
	return filepath.Join(PathCropped, queryID, fmt.Sprintf("%s-cropped.png", chunkID))
}
