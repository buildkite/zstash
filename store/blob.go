package store

import (
	"context"
	"fmt"
	"net/url"
)

// Blob interface defines the operations for blob storage
type Blob interface {
	// Upload uploads a file to blob storage
	Upload(ctx context.Context, filePath string, key string) (*TransferInfo, error)

	// Download downloads a file from blob storage
	Download(ctx context.Context, key string, destPath string) (*TransferInfo, error)
}

func NewBlobStore(ctx context.Context, store string, bucketURL string) (Blob, error) {
	switch store {
	case LocalS3Store:
		return NewS3Blob(ctx, bucketURL)
	case LocalHostedAgents:
		return NewNscStore()
	default:
		return nil, fmt.Errorf("unsupported store type: %s", store)
	}
}

// Supported blob storage schemes
var supportedSchemes = map[string]bool{
	"s3": true, // AWS S3
}

// extract the service from a bucket URL and validate it's supported
func extractServiceFromBucketURL(bucketURL string) (string, error) {
	u, err := url.Parse(bucketURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse bucket URL: %w", err)
	}

	scheme := u.Scheme
	if scheme == "" {
		return "", fmt.Errorf("bucket URL must have a scheme (e.g., s3://, gs://, azblob://)")
	}

	if !supportedSchemes[scheme] {
		return "", fmt.Errorf("unsupported URL scheme %q: must be one of s3, gs, azblob, file, or mem", scheme)
	}

	return scheme, nil
}
