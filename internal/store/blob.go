package store

import (
	"context"
	"time"
)

// Blob interface defines the operations for blob storage
type Blob interface {
	// Upload uploads a file to blob storage
	Upload(ctx context.Context, filePath string, key string, expiresAt time.Time) (*TransferInfo, error)

	// Download downloads a file from blob storage
	Download(ctx context.Context, key string, destPath string) (*TransferInfo, error)
}
