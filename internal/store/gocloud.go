package store

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/buildkite/zstash/internal/trace"
	"go.opentelemetry.io/otel/attribute"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob" // Local file driver for testing
	_ "gocloud.dev/blob/s3blob"   // AWS S3 driver
)

// GocloudBlob implements the Blob interface using gocloud.dev
type GocloudBlob struct {
	bucket *blob.Bucket
	prefix string
}

// Ensure GocloudBlob implements the Blob interface
var _ Blob = (*GocloudBlob)(nil)

// NewGocloudBlob creates a new GocloudBlob instance using a blob URL and prefix
// For S3: "s3://bucket-name?region=us-east-1"
// For local development: "file:///path/to/directory"
// For GCS: "gs://bucket-name"
// For Azure: "azblob://bucket-name"
func NewGocloudBlob(ctx context.Context, blobURL, prefix string) (*GocloudBlob, error) {
	// Normalize the prefix to ensure it has the correct format
	normalizedPrefix := normalizePrefix(prefix)

	// Open the bucket using gocloud.dev
	bucket, err := blob.OpenBucket(ctx, blobURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open blob bucket: %w", err)
	}

	return &GocloudBlob{
		bucket: bucket,
		prefix: normalizedPrefix,
	}, nil
}

// Close closes the underlying bucket connection
func (b *GocloudBlob) Close() error {
	return b.bucket.Close()
}

// Upload uploads a file to blob storage
func (b *GocloudBlob) Upload(ctx context.Context, filePath string, key string) (*TransferInfo, error) {
	ctx, span := trace.Start(ctx, "GocloudBlob.Upload")
	defer span.End()

	start := time.Now()

	// Get the full key with prefix
	fullKey := b.getFullKey(key)

	// Open the source file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer file.Close()

	// Get file size for validation (optional)
	_, err = file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file %s: %w", filePath, err)
	}

	// Create a writer for the blob
	writer, err := b.bucket.NewWriter(ctx, fullKey, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create blob writer: %w", err)
	}

	// Copy file content to blob
	bytesWritten, err := io.Copy(writer, file)
	if err != nil {
		writer.Close() // Close writer on error
		return nil, fmt.Errorf("failed to copy file to blob: %w", err)
	}

	// Close the writer to commit the upload
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close blob writer: %w", err)
	}

	duration := time.Since(start)
	averageSpeed := calculateTransferSpeedMBps(bytesWritten, duration)

	span.SetAttributes(
		attribute.Int64("bytes_transferred", bytesWritten),
		attribute.String("transfer_speed", fmt.Sprintf("%.2fMB/s", averageSpeed)),
		attribute.String("blob_key", fullKey),
	)

	return &TransferInfo{
		BytesTransferred: bytesWritten,
		TransferSpeed:    averageSpeed,
		RequestID:        "", // gocloud.dev doesn't expose request IDs in the same way
		Duration:         duration,
	}, nil
}

// Download downloads a file from blob storage
func (b *GocloudBlob) Download(ctx context.Context, key string, destPath string) (*TransferInfo, error) {
	ctx, span := trace.Start(ctx, "GocloudBlob.Download")
	defer span.End()

	start := time.Now()

	// Get the full key with prefix
	fullKey := b.getFullKey(key)

	// Create a reader for the blob
	reader, err := b.bucket.NewReader(ctx, fullKey, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create blob reader: %w", err)
	}
	defer reader.Close()

	// Create the destination file
	destFile, err := os.Create(destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create destination file %s: %w", destPath, err)
	}
	defer destFile.Close()

	// Copy blob content to file
	bytesWritten, err := io.Copy(destFile, reader)
	if err != nil {
		return nil, fmt.Errorf("failed to copy blob to file: %w", err)
	}

	duration := time.Since(start)
	averageSpeed := calculateTransferSpeedMBps(bytesWritten, duration)

	span.SetAttributes(
		attribute.Int64("bytes_transferred", bytesWritten),
		attribute.String("transfer_speed", fmt.Sprintf("%.2fMB/s", averageSpeed)),
		attribute.String("blob_key", fullKey),
	)

	return &TransferInfo{
		BytesTransferred: bytesWritten,
		TransferSpeed:    averageSpeed,
		RequestID:        "", // gocloud.dev doesn't expose request IDs in the same way
		Duration:         duration,
	}, nil
}

// getFullKey combines the prefix with the key (reused from s3.go)
func (b *GocloudBlob) getFullKey(key string) string {
	// Remove leading slash from key if present
	key = strings.TrimPrefix(key, "/")
	// Combine prefix and key
	return path.Join(b.prefix, key)
}
