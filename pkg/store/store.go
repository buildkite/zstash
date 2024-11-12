package store

import (
	"context"
	"fmt"
	"io"
	"os"
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

// GoLang: os.Rename() give error "invalid cross-device link" for Docker container with Volumes.
// MoveFile(source, destination) will work moving file between folders
//
// https://gist.github.com/var23rav/23ae5d0d4d830aff886c3c970b8f6c6b
func MoveFile(sourcePath, destPath string) error {
	inputFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("couldn't open source file: %w", err)
	}
	outputFile, err := os.Create(destPath)
	if err != nil {
		inputFile.Close()
		return fmt.Errorf("couldn't open dest file: %w", err)
	}
	defer outputFile.Close()
	_, err = io.Copy(outputFile, inputFile)
	inputFile.Close()
	if err != nil {
		return fmt.Errorf("writing to output file failed: %w", err)
	}
	// The copy was successful, so now delete the original file
	err = os.Remove(sourcePath)
	if err != nil {
		return fmt.Errorf("failed removing original file: %w", err)
	}
	return nil
}
