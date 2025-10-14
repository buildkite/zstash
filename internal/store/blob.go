package store

import (
	"context"
)

// Blob interface defines the operations for blob storage
type Blob interface {
	// Upload uploads a file to blob storage
	Upload(ctx context.Context, filePath string, key string) (*TransferInfo, error)

	// Download downloads a file from blob storage
	Download(ctx context.Context, key string, destPath string) (*TransferInfo, error)
}
