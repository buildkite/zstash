package zstash

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/buildkite/zstash/api"
	"github.com/buildkite/zstash/archive"
	"github.com/buildkite/zstash/store"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// Restore restores a cache from storage by ID.
//
// The function performs the following workflow:
//  1. Validates the cache configuration
//  2. Checks if the cache exists (tries exact key, then fallback keys)
//  3. Downloads the cache archive from cloud storage
//  4. Extracts files to their original paths
//  5. Cleans up temporary files
//
// If no matching cache is found (including fallback keys), the function returns
// early with CacheRestored=false. This is not an error condition.
//
// The operation respects context cancellation and will stop immediately when
// ctx is cancelled, cleaning up any temporary resources (downloaded archives).
//
// Progress callbacks (if configured) are invoked at each stage with the
// following stages: "validating", "checking_exists", "downloading", "extracting",
// "complete".
//
// Returns RestoreResult with detailed metrics, or an error if the operation failed.
//
// Use RestoreResult.CacheHit to check if the exact key matched, and
// RestoreResult.FallbackUsed to check if a fallback key was used.
//
// Example:
//
//	result, err := cacheClient.Restore(ctx, "node_modules")
//	if err != nil {
//	    log.Fatalf("Cache restore failed: %v", err)
//	}
//	if !result.CacheRestored {
//	    log.Printf("Cache miss for key: %s", result.Key)
//	} else if result.FallbackUsed {
//	    log.Printf("Restored from fallback key: %s", result.Key)
//	} else {
//	    log.Printf("Cache hit: %s (%.2f MB)", result.Key, float64(result.Archive.Size)/(1024*1024))
//	}
func (c *Cache) Restore(ctx context.Context, cacheID string) (RestoreResult, error) {
	tracer := otel.Tracer("github.com/buildkite/zstash")
	ctx, span := tracer.Start(ctx, "Cache.Restore")
	defer span.End()

	span.SetAttributes(
		attribute.String("cache.id", cacheID),
		attribute.String("cache.branch", c.branch),
		attribute.String("cache.pipeline", c.pipeline),
		attribute.String("cache.organization", c.organization),
		attribute.String("cache.platform", c.platform),
	)

	startTime := time.Now()
	result := RestoreResult{}

	// Find the cache configuration
	cacheConfig, err := c.findCache(cacheID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to find cache configuration")
		return result, err
	}

	result.Key = cacheConfig.Key

	span.SetAttributes(
		attribute.String("cache.key", cacheConfig.Key),
		attribute.String("cache.registry", c.registry),
		attribute.StringSlice("cache.fallback_keys", cacheConfig.FallbackKeys),
		attribute.Int("cache.paths_count", len(cacheConfig.Paths)),
	)

	c.callProgress("validating", "Validating cache configuration", 0, 0)

	c.callProgress("checking_exists", "Checking if cache exists", 0, 0)

	// Check if cache exists
	retrieveResp, exists, err := c.client.CacheRetrieve(ctx, c.registry, api.CacheRetrieveReq{
		Key:          cacheConfig.Key,
		Branch:       c.branch,
		FallbackKeys: strings.Join(cacheConfig.FallbackKeys, ","),
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to retrieve cache")
		return result, fmt.Errorf("failed to retrieve cache: %w", err)
	}

	if !exists {
		// Cache miss
		result.CacheHit = false
		result.CacheRestored = false
		result.TotalDuration = time.Since(startTime)
		span.SetAttributes(
			attribute.Bool("cache.hit", false),
			attribute.Bool("cache.restored", false),
			attribute.Int64("cache.duration_ms", result.TotalDuration.Milliseconds()),
		)
		span.SetStatus(codes.Ok, "cache miss")
		c.callProgress("complete", "Cache miss", 0, 0)
		return result, nil
	}

	// Cache found (either exact match or fallback)
	result.Key = retrieveResp.Key
	result.FallbackUsed = retrieveResp.Fallback
	result.CacheHit = !retrieveResp.Fallback
	result.ExpiresAt = retrieveResp.ExpiresAt

	span.SetAttributes(
		attribute.Bool("cache.fallback_used", result.FallbackUsed),
		attribute.String("cache.matched_key", result.Key),
	)

	c.callProgress("downloading", "Downloading cache archive", 0, 0)

	// Download cache
	tmpDir, archiveFile, transferInfo, err := c.downloadCache(ctx, retrieveResp, c.bucketURL)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to download cache")
		return result, fmt.Errorf("failed to download cache: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	// Populate transfer metrics
	result.Transfer = TransferMetrics{
		BytesTransferred: transferInfo.BytesTransferred,
		TransferSpeed:    transferInfo.TransferSpeed,
		Duration:         transferInfo.Duration,
		RequestID:        transferInfo.RequestID,
	}

	c.callProgress("extracting", "Extracting files from cache", 0, int(transferInfo.BytesTransferred))

	// Extract files
	archiveInfo, err := c.extractCache(ctx, archiveFile, transferInfo.BytesTransferred, cacheConfig.Paths)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to extract cache")
		return result, fmt.Errorf("failed to extract cache: %w", err)
	}

	// Populate archive metrics
	result.Archive = ArchiveMetrics{
		Size:             archiveInfo.Size,
		WrittenBytes:     archiveInfo.WrittenBytes,
		WrittenEntries:   archiveInfo.WrittenEntries,
		CompressionRatio: float64(archiveInfo.WrittenBytes) / float64(archiveInfo.Size),
		Duration:         archiveInfo.Duration,
		Paths:            cacheConfig.Paths,
	}

	result.CacheRestored = true
	result.TotalDuration = time.Since(startTime)

	// Add result attributes to span
	span.SetAttributes(
		attribute.Bool("cache.hit", result.CacheHit),
		attribute.Bool("cache.restored", result.CacheRestored),
		attribute.Int64("cache.archive_size_bytes", result.Archive.Size),
		attribute.Int64("cache.written_bytes", result.Archive.WrittenBytes),
		attribute.Int64("cache.written_entries", result.Archive.WrittenEntries),
		attribute.Float64("cache.compression_ratio", result.Archive.CompressionRatio),
		attribute.Int64("cache.transfer_bytes", result.Transfer.BytesTransferred),
		attribute.Float64("cache.transfer_speed_mbps", result.Transfer.TransferSpeed),
		attribute.Int64("cache.duration_ms", result.TotalDuration.Milliseconds()),
	)
	span.SetStatus(codes.Ok, "cache restored successfully")

	c.callProgress("complete", "Cache restored successfully", 0, 0)

	return result, nil
}

// downloadCache downloads a cache archive from storage
func (c *Cache) downloadCache(ctx context.Context, retrieveResp api.CacheRetrieveResp, bucketURL string) (tmpDir string, archiveFile string, transferInfo *store.TransferInfo, err error) {
	tracer := otel.Tracer("github.com/buildkite/zstash")
	ctx, span := tracer.Start(ctx, "Cache.downloadCache")
	defer span.End()

	span.SetAttributes(
		attribute.String("cache.store_type", retrieveResp.Store),
		attribute.String("cache.object_name", retrieveResp.StoreObjectName),
	)

	// Create blob store
	blobStore, err := store.NewBlobStore(ctx, retrieveResp.Store, bucketURL)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create blob store")
		return "", "", nil, fmt.Errorf("failed to create blob store: %w", err)
	}

	// Create temporary directory
	tmpDir, err = os.MkdirTemp("", "zstash-restore")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create temp directory")
		return "", "", nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	archiveFile = filepath.Join(tmpDir, retrieveResp.StoreObjectName)

	// Download archive
	transferInfo, err = blobStore.Download(ctx, retrieveResp.StoreObjectName, archiveFile)
	if err != nil {
		// Clean up temporary directory on failure
		_ = os.RemoveAll(tmpDir)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to download from blob store")
		return "", "", nil, fmt.Errorf("failed to download cache: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("cache.bytes_transferred", transferInfo.BytesTransferred),
		attribute.Float64("cache.transfer_speed_mbps", transferInfo.TransferSpeed),
		attribute.String("cache.request_id", transferInfo.RequestID),
	)
	span.SetStatus(codes.Ok, "download completed")

	return tmpDir, archiveFile, transferInfo, nil
}

// extractCache extracts files from a cache archive
func (c *Cache) extractCache(ctx context.Context, archiveFile string, archiveSize int64, paths []string) (*archive.ArchiveInfo, error) {
	tracer := otel.Tracer("github.com/buildkite/zstash")
	ctx, span := tracer.Start(ctx, "Cache.extractCache")
	defer span.End()

	span.SetAttributes(
		attribute.String("cache.archive_file", archiveFile),
		attribute.Int64("cache.archive_size_bytes", archiveSize),
		attribute.Int("cache.paths_count", len(paths)),
	)

	// Open archive file
	archiveFileHandle, err := os.Open(archiveFile)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to open archive file")
		return nil, fmt.Errorf("failed to open archive file: %w", err)
	}
	defer archiveFileHandle.Close()

	// Extract files
	archiveInfo, err := archive.ExtractFiles(ctx, archiveFileHandle, archiveSize, paths)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to extract archive")
		return nil, fmt.Errorf("failed to extract archive: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("cache.written_bytes", archiveInfo.WrittenBytes),
		attribute.Int64("cache.written_entries", archiveInfo.WrittenEntries),
	)
	span.SetStatus(codes.Ok, "extraction completed")

	return archiveInfo, nil
}
