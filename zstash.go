package zstash

import (
	"errors"
	"time"

	"github.com/buildkite/zstash/api"
	"github.com/buildkite/zstash/cache"
)

// Sentinel errors for common scenarios
var (
	// ErrCacheNotFound is returned when a cache ID doesn't exist in the service
	ErrCacheNotFound = errors.New("cache not found")

	// ErrInvalidConfiguration is returned when configuration validation fails
	ErrInvalidConfiguration = errors.New("invalid configuration")
)

// Cache provides cache save and restore operations.
// Create a cache once with your configuration, then use it for multiple operations.
type Cache struct {
	client       api.Client
	bucketURL    string
	format       string
	branch       string
	pipeline     string
	organization string
	platform     string
	caches       []cache.Cache
	onProgress   ProgressCallback
}

// Config holds all configuration for creating a Cache
type Config struct {
	// Client is the Buildkite API client (required)
	Client api.Client

	// BucketURL is the storage backend URL (e.g., "s3://bucket-name")
	BucketURL string

	// Format is the archive format (defaults to "zip")
	Format string

	// Branch is the git branch name (used for cache scoping)
	Branch string

	// Pipeline is the pipeline slug (used for cache scoping)
	Pipeline string

	// Organization is the organization slug (used for cache scoping)
	Organization string

	// Platform is the OS/arch string (e.g., "linux/amd64", "darwin/arm64")
	// If empty, defaults to runtime.GOOS/runtime.GOARCH
	Platform string

	// Env is the environment variable map used for cache template expansion
	// If nil, caches must already be expanded
	Env map[string]string

	// Caches is the list of cache configurations to manage
	Caches []cache.Cache

	// OnProgress is an optional callback for progress updates during operations
	OnProgress ProgressCallback
}

// ProgressCallback is called during long-running operations to report progress.
//
// Parameters:
//   - stage: The current operation stage (e.g., "validating", "building_archive",
//     "uploading", "downloading", "extracting")
//   - message: A human-readable description of the current action
//   - current: Current progress value (bytes transferred, files processed, etc.)
//   - total: Total expected value (0 if unknown)
type ProgressCallback func(stage string, message string, current int, total int)

// NewCache creates and validates a new cache client.
// Implementation is in service.go

// Save, SaveAll, Restore, and RestoreAll methods are implemented in save.go and restore.go

// ListCaches returns all cache configurations managed by this cache.
// These caches have already been expanded and validated.
func (c *Cache) ListCaches() []cache.Cache {
	return c.caches
}

// GetCache returns a specific cache configuration by ID.
// Returns an error if the cache ID is not found.
func (c *Cache) GetCache(id string) (cache.Cache, error) {
	for _, cacheItem := range c.caches {
		if cacheItem.ID == id {
			return cacheItem, nil
		}
	}
	return cache.Cache{}, ErrCacheNotFound
}

// SaveResult contains detailed information about a cache save operation
type SaveResult struct {
	// Error is any error that occurred during this specific cache operation.
	// If nil, the operation succeeded.
	Error error

	// CacheCreated indicates whether a new cache entry was created.
	// false means the cache already existed and no upload occurred.
	CacheCreated bool

	// Key is the actual cache key that was used (after template expansion)
	Key string

	// Registry is the cache registry that was used
	Registry string

	// UploadID is the unique identifier for this upload (if created)
	UploadID string

	// Archive contains information about the archive that was built
	Archive ArchiveMetrics

	// Transfer contains information about the upload (if performed)
	Transfer *TransferMetrics // nil if cache already existed

	// TotalDuration is the end-to-end duration of the save operation
	TotalDuration time.Duration
}

// RestoreResult contains detailed information about a cache restore operation
type RestoreResult struct {
	// Error is any error that occurred during this specific cache operation.
	// If nil, the operation succeeded.
	Error error

	// CacheHit indicates whether the exact cache key was found.
	// false means either no cache found, or a fallback key was used.
	CacheHit bool

	// CacheRestored indicates whether any cache was restored (including fallbacks).
	// false means complete cache miss.
	CacheRestored bool

	// Key is the actual cache key that was restored (may be a fallback key)
	Key string

	// Registry is the cache registry that was used
	Registry string

	// FallbackUsed indicates whether a fallback key was used
	FallbackUsed bool

	// Archive contains information about the archive that was extracted
	Archive ArchiveMetrics

	// Transfer contains information about the download
	Transfer TransferMetrics

	// ExpiresAt indicates when this cache entry will expire
	ExpiresAt time.Time

	// TotalDuration is the end-to-end duration of the restore operation
	TotalDuration time.Duration
}

// ArchiveMetrics contains metrics about archive operations
type ArchiveMetrics struct {
	// Size is the total size of the archive file in bytes
	Size int64

	// WrittenBytes is the uncompressed size of all files in bytes
	WrittenBytes int64

	// WrittenEntries is the number of files/directories in the archive
	WrittenEntries int64

	// CompressionRatio is WrittenBytes / Size (higher = better compression)
	CompressionRatio float64

	// Sha256Sum is the SHA-256 hash of the archive (for save operations)
	Sha256Sum string

	// Duration is how long the archive operation took
	Duration time.Duration

	// Paths are the filesystem paths that were archived/extracted
	Paths []string
}

// TransferMetrics contains metrics about upload/download operations
type TransferMetrics struct {
	// BytesTransferred is the number of bytes uploaded or downloaded
	BytesTransferred int64

	// TransferSpeed is the transfer rate in MB/s
	TransferSpeed float64

	// Duration is how long the transfer took
	Duration time.Duration

	// RequestID is the provider-specific request identifier (for debugging)
	RequestID string
}
