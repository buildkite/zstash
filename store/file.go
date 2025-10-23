package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/buildkite/zstash/internal/trace"
	"go.opentelemetry.io/otel/attribute"
)

const (
	metadataSuffix = ".attrs.json"
)

// LocalFileBlob implements the Blob interface for local filesystem storage.
// It stores cache artifacts as files on the local filesystem with accompanying
// metadata sidecar files. The storage structure directly maps cache keys to
// file paths under the configured root directory.
//
// Storage layout:
//   - Data files: <root>/<key>
//   - Metadata files: <root>/<key>.attrs.json
//
// This implementation provides:
//   - Atomic writes via temp file + rename
//   - Path traversal protection with multi-layer validation
//   - SHA256 integrity checksums
//   - Last-writer-wins semantics for concurrent updates
type LocalFileBlob struct {
	root string // Absolute path to the root storage directory
}

// FileMetadata contains metadata for cached files stored in the local filesystem.
// This metadata is persisted as JSON in a sidecar file alongside the data file.
type FileMetadata struct {
	Key       string `json:"key"`              // Original cache key
	Size      int64  `json:"size"`             // File size in bytes
	ModTime   string `json:"mod_time"`         // Original modification time (RFC3339Nano)
	Mode      string `json:"mode"`             // Original file permissions (octal)
	SHA256    string `json:"sha256,omitempty"` // SHA256 checksum for integrity verification
	CreatedAt string `json:"created_at"`       // Timestamp when cached (RFC3339Nano)
	Version   int    `json:"version"`          // Metadata schema version
}

// NewLocalFileBlob creates a new local file storage backend from a file:// URL.
//
// Supported URL formats:
//   - file:///absolute/path/to/cache
//   - file://~/cache (expands to user's home directory)
//   - file://~/.buildkitecache
//
// The root directory will be created if it doesn't exist.
//
// Returns an error if:
//   - URL scheme is not "file"
//   - Path is empty or invalid (e.g., "/", ".")
//   - Directory creation fails
func NewLocalFileBlob(ctx context.Context, fileURL string) (*LocalFileBlob, error) {
	u, err := url.Parse(fileURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse file URL: %w", err)
	}

	if u.Scheme != "file" {
		return nil, fmt.Errorf("invalid URL scheme %q: must be file", u.Scheme)
	}

	path := u.Path
	if path == "" {
		return nil, fmt.Errorf("file URL path cannot be empty")
	}

	// Handle tilde expansion for home directory
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %w", err)
		}
		// Remove ~ or ~/ prefix and join with home directory
		path = strings.TrimPrefix(path, "~")
		path = strings.TrimPrefix(path, "/")
		path = filepath.Join(homeDir, path)
	}

	root := filepath.Clean(filepath.FromSlash(path))
	if root == "" || root == "/" || root == "." {
		return nil, fmt.Errorf("invalid root directory: %s", root)
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create root directory: %w", err)
	}

	slog.Debug("configured local file store", "root", root)

	return &LocalFileBlob{root: root}, nil
}

// Upload copies a file from srcPath to the cache storage identified by key.
//
// The upload process:
//  1. Validates the source path and cache key
//  2. Computes SHA256 hash during copy for integrity verification
//  3. Writes data atomically via temp file + fsync + rename
//  4. Writes metadata (size, permissions, timestamps, checksum) to sidecar file
//  5. Fsyncs parent directory for durability (best-effort)
//
// On Windows, existing files are removed before rename due to platform limitations.
// This creates a last-writer-wins semantic for concurrent uploads to the same key.
//
// Returns TransferInfo containing bytes transferred, transfer speed, and duration.
func (b *LocalFileBlob) Upload(ctx context.Context, srcPath string, key string) (*TransferInfo, error) {
	_, span := trace.Start(ctx, "LocalFileBlob.Upload")
	defer span.End()

	start := time.Now()

	if err := validateFilePath(srcPath); err != nil {
		return nil, fmt.Errorf("invalid source path: %w", err)
	}

	dataPath, metaPath, err := b.keyToPaths(key)
	if err != nil {
		return nil, err
	}

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open source file %s: %w", srcPath, err)
	}
	defer func() {
		_ = srcFile.Close()
	}()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat source file: %w", err)
	}

	// Create temp file in same directory as final destination for atomic rename
	tmpFile, err := os.CreateTemp(filepath.Dir(dataPath), ".zstash-upload-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpData := tmpFile.Name()

	cleanup := true
	defer func() {
		_ = tmpFile.Close()
		if cleanup {
			_ = os.Remove(tmpData)
		}
	}()

	// Compute SHA256 hash during copy for integrity verification
	// Note: For large files (GB-scale), this adds CPU overhead. Consider making
	// this optional via configuration if performance becomes an issue.
	hash := sha256.New()
	teeReader := io.TeeReader(srcFile, hash)

	bytesWritten, err := io.Copy(tmpFile, teeReader)
	if err != nil {
		return nil, fmt.Errorf("failed to copy data: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		return nil, fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temp file: %w", err)
	}

	// Remove existing file before rename (required for Windows atomicity)
	if err := os.Remove(dataPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to remove existing file: %w", err)
	}

	if err := os.Rename(tmpData, dataPath); err != nil {
		return nil, fmt.Errorf("failed to rename temp file: %w", err)
	}

	cleanup = false

	// Fsync parent directory for durability (optional but recommended)
	if dir, err := os.Open(filepath.Dir(dataPath)); err == nil {
		if err := dir.Sync(); err != nil {
			slog.Warn("failed to fsync directory after upload", "path", filepath.Dir(dataPath), "error", err)
		}
		_ = dir.Close()
	}

	metadata := FileMetadata{
		Key:       key,
		Size:      bytesWritten,
		ModTime:   srcInfo.ModTime().Format(time.RFC3339Nano),
		Mode:      fmt.Sprintf("%04o", srcInfo.Mode().Perm()),
		SHA256:    hex.EncodeToString(hash.Sum(nil)),
		CreatedAt: time.Now().Format(time.RFC3339Nano),
		Version:   1,
	}

	// Create temp metadata file in same directory as final destination
	metaFile, err := os.CreateTemp(filepath.Dir(metaPath), ".zstash-meta-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp metadata file: %w", err)
	}
	tmpMeta := metaFile.Name()

	cleanupMeta := true
	defer func() {
		_ = metaFile.Close()
		if cleanupMeta {
			_ = os.Remove(tmpMeta)
		}
	}()

	if err := json.NewEncoder(metaFile).Encode(metadata); err != nil {
		return nil, fmt.Errorf("failed to write metadata: %w", err)
	}

	if err := metaFile.Sync(); err != nil {
		return nil, fmt.Errorf("failed to sync metadata file: %w", err)
	}

	if err := metaFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close metadata file: %w", err)
	}

	// Remove existing metadata file before rename (required for Windows atomicity)
	if err := os.Remove(metaPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to remove existing metadata file: %w", err)
	}

	if err := os.Rename(tmpMeta, metaPath); err != nil {
		return nil, fmt.Errorf("failed to rename metadata file: %w", err)
	}

	cleanupMeta = false

	duration := time.Since(start)
	averageSpeed := calculateTransferSpeedMBps(bytesWritten, duration)

	span.SetAttributes(
		attribute.Int64("bytes_transferred", bytesWritten),
		attribute.String("transfer_speed", fmt.Sprintf("%.2fMB/s", averageSpeed)),
		attribute.String("key", key),
	)

	return &TransferInfo{
		BytesTransferred: bytesWritten,
		TransferSpeed:    averageSpeed,
		RequestID:        "",
		Duration:         duration,
	}, nil
}

// Download retrieves a cached file identified by key and writes it to destPath.
//
// The download process:
//  1. Validates the cache key and destination path
//  2. Reads the cached file from storage
//  3. Writes to destination atomically via temp file + fsync + rename
//  4. Attempts to restore original file metadata (mtime, permissions) if available
//  5. Fsyncs parent directory for durability (best-effort)
//
// Metadata restoration is best-effort and failures are logged but don't fail the download.
//
// Returns TransferInfo containing bytes transferred, transfer speed, and duration.
// Returns an error if the cache key doesn't exist or if file operations fail.
func (b *LocalFileBlob) Download(ctx context.Context, key string, destPath string) (*TransferInfo, error) {
	_, span := trace.Start(ctx, "LocalFileBlob.Download")
	defer span.End()

	start := time.Now()

	if err := validateFilePath(destPath); err != nil {
		return nil, fmt.Errorf("invalid destination path: %w", err)
	}

	dataPath, metaPath, err := b.keyToPaths(key)
	if err != nil {
		return nil, err
	}

	srcFile, err := os.Open(dataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open source file: %w", err)
	}
	defer func() {
		_ = srcFile.Close()
	}()

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Create temp file in same directory as final destination for atomic rename
	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), ".zstash-download-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpDest := tmpFile.Name()

	cleanup := true
	defer func() {
		_ = tmpFile.Close()
		if cleanup {
			_ = os.Remove(tmpDest)
		}
	}()

	bytesWritten, err := io.Copy(tmpFile, srcFile)
	if err != nil {
		return nil, fmt.Errorf("failed to copy data: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		return nil, fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temp file: %w", err)
	}

	_ = os.Remove(destPath)

	if err := os.Rename(tmpDest, destPath); err != nil {
		return nil, fmt.Errorf("failed to rename temp file: %w", err)
	}

	cleanup = false

	// Fsync parent directory for durability (optional but recommended)
	if dir, err := os.Open(filepath.Dir(destPath)); err == nil {
		if err := dir.Sync(); err != nil {
			slog.Warn("failed to fsync directory after download", "path", filepath.Dir(destPath), "error", err)
		}
		_ = dir.Close()
	}

	// Attempt to restore metadata if available (best-effort)
	if metaData, err := os.ReadFile(metaPath); err == nil {
		var metadata FileMetadata
		if err := json.Unmarshal(metaData, &metadata); err == nil {
			if metadata.ModTime != "" {
				if modTime, err := time.Parse(time.RFC3339Nano, metadata.ModTime); err == nil {
					_ = os.Chtimes(destPath, time.Now(), modTime)
				}
			}
		} else {
			slog.Warn("failed to parse metadata file", "path", metaPath, "error", err)
		}
	}

	duration := time.Since(start)
	averageSpeed := calculateTransferSpeedMBps(bytesWritten, duration)

	span.SetAttributes(
		attribute.Int64("bytes_transferred", bytesWritten),
		attribute.String("transfer_speed", fmt.Sprintf("%.2fMB/s", averageSpeed)),
		attribute.String("key", key),
	)

	return &TransferInfo{
		BytesTransferred: bytesWritten,
		TransferSpeed:    averageSpeed,
		RequestID:        "",
		Duration:         duration,
	}, nil
}

func (b *LocalFileBlob) keyToPaths(key string) (dataPath, metaPath string, err error) {
	if err := validateFileKey(key); err != nil {
		return "", "", err
	}

	k := strings.TrimPrefix(key, "/")
	k = filepath.Clean(filepath.FromSlash(k))

	if k == "." || k == "" {
		return "", "", fmt.Errorf("invalid key: resolves to empty path")
	}

	dataPath = filepath.Join(b.root, k)

	rel, err := filepath.Rel(b.root, dataPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to compute relative path: %w", err)
	}
	if strings.HasPrefix(rel, "..") {
		return "", "", fmt.Errorf("key escapes root directory")
	}

	metaPath = dataPath + metadataSuffix

	if err := os.MkdirAll(filepath.Dir(dataPath), 0o755); err != nil {
		return "", "", fmt.Errorf("failed to create parent directory: %w", err)
	}

	return dataPath, metaPath, nil
}

func validateFileKey(key string) error {
	if key == "" {
		return fmt.Errorf("key cannot be empty")
	}

	if len(key) > 512 {
		return fmt.Errorf("key too long (max 512 characters)")
	}

	validKeyPattern := regexp.MustCompile(`^[a-zA-Z0-9._/-]+$`)
	if !validKeyPattern.MatchString(key) {
		return fmt.Errorf("key contains invalid characters (only alphanumeric, ., _, /, - are allowed)")
	}

	dangerousPatterns := []string{"../", "/./", "//", "&&", "||", ";", "`", "$"}
	for _, pattern := range dangerousPatterns {
		if strings.Contains(key, pattern) {
			return fmt.Errorf("key contains potentially dangerous pattern: %s", pattern)
		}
	}

	return nil
}
