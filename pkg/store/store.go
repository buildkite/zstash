package store

import (
	"context"
	"fmt"
	"net/url"
)

type Store interface {
	Download(ctx context.Context, remoteCacheURL, path string) error
	Upload(ctx context.Context, remoteCacheURL, path string, expiresInSecs int64) error
}

// Download downloads a file from a remote cache URL to a local path.
func Download(ctx context.Context, remoteCacheURL, path string) error {

	u, err := url.Parse(remoteCacheURL)
	if err != nil {
		return fmt.Errorf("failed to parse remote cache url=%s", remoteCacheURL)
	}

	switch u.Scheme {
	case "s3":
		s3Store, err := NewS3Store()
		if err != nil {
			return fmt.Errorf("failed to create s3 store: %w", err)
		}

		return s3Store.Download(ctx, remoteCacheURL, path)
	default:
		return fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
}

// Upload uploads a file from a local path to a remote cache URL.
func Upload(ctx context.Context, remoteCacheURL, path string, expiresInSecs int64) error {

	u, err := url.Parse(remoteCacheURL)
	if err != nil {
		return fmt.Errorf("failed to parse remote cache url=%s", remoteCacheURL)
	}

	switch u.Scheme {
	case "s3":
		s3Store, err := NewS3Store()
		if err != nil {
			return fmt.Errorf("failed to create s3 store: %w", err)
		}

		return s3Store.Upload(ctx, remoteCacheURL, path, expiresInSecs)
	default:
		return fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
}
