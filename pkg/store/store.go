package store

import (
	"context"
	"fmt"
)

var (
	// ErrNotFound is returned when the requested object is not found.
	ErrNotFound = fmt.Errorf("cache key not found in remote cache")
)

type Store interface {
	// Exists checks if the object exists in the remote cache.
	Exists(ctx context.Context, remoteCacheURL, path string) (string, bool, error)
	// Download downloads the object from the remote cache to the local cache.
	Download(ctx context.Context, remoteCacheURL, path, sha256sum string) error
	// Upload uploads the object from the local cache to the remote cache.
	Upload(ctx context.Context, remoteCacheURL, path, sha256sum string, expiresInSecs int64) error
}
