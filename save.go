package zstash

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/buildkite/zstash/api"
	"github.com/buildkite/zstash/archive"
	"github.com/buildkite/zstash/store"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// Save saves a cache to storage by ID.
//
// The function performs the following workflow:
//  1. Validates the cache configuration and paths exist
//  2. Checks if the cache already exists (early return if yes)
//  3. Builds an archive of the cache paths
//  4. Creates a cache entry in the Buildkite API
//  5. Uploads the archive to cloud storage
//  6. Commits the cache entry
//
// If the cache already exists, no upload is performed and the function returns
// early with CacheCreated=false and Transfer=nil.
//
// The operation respects context cancellation and will stop immediately when
// ctx is cancelled, cleaning up any temporary resources.
//
// Progress callbacks (if configured) are invoked at each stage with the
// following stages: "validating", "checking_exists", "fetching_registry",
// "building_archive", "creating_entry", "uploading", "committing", "complete".
//
// Returns SaveResult with detailed metrics, or an error if the operation failed.
//
// Example:
//
//	result, err := cacheClient.Save(ctx, "node_modules")
//	if err != nil {
//	    log.Fatalf("Cache save failed: %v", err)
//	}
//	if !result.CacheCreated {
//	    log.Printf("Cache already exists for key: %s", result.Key)
//	} else {
//	    log.Printf("Cache saved: %s (%.2f MB)", result.Key, float64(result.Archive.Size)/(1024*1024))
//	}
func (c *Cache) Save(ctx context.Context, cacheID string) (SaveResult, error) {
	tracer := otel.Tracer("github.com/buildkite/zstash")
	ctx, span := tracer.Start(ctx, "Cache.Save")
	defer span.End()

	span.SetAttributes(
		attribute.String("cache.id", cacheID),
		attribute.String("cache.branch", c.branch),
		attribute.String("cache.pipeline", c.pipeline),
		attribute.String("cache.organization", c.organization),
		attribute.String("cache.platform", c.platform),
		attribute.String("cache.format", c.format),
	)

	startTime := time.Now()
	result := SaveResult{}

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
		attribute.Int("cache.paths_count", len(cacheConfig.Paths)),
		attribute.Int("cache.fallback_keys_count", len(cacheConfig.FallbackKeys)),
	)

	c.callProgress("validating", "Validating cache configuration", 0, 0)

	// Validate cache paths exist
	if err := checkPathsExist(cacheConfig.Paths); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid cache paths")
		return result, fmt.Errorf("invalid cache paths: %w", err)
	}

	c.callProgress("checking_exists", "Checking if cache already exists", 0, 0)

	// Check if cache already exists
	_, exists, err := c.client.CachePeekExists(ctx, c.registry, api.CachePeekReq{
		Key:    cacheConfig.Key,
		Branch: c.branch,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to check cache existence")
		return result, fmt.Errorf("failed to check cache existence: %w", err)
	}

	if exists {
		// Cache already exists, no need to upload
		result.CacheCreated = false
		result.TotalDuration = time.Since(startTime)
		span.SetAttributes(
			attribute.Bool("cache.created", false),
			attribute.Bool("cache.already_exists", true),
			attribute.Int64("cache.duration_ms", result.TotalDuration.Milliseconds()),
		)
		span.SetStatus(codes.Ok, "cache already exists")
		c.callProgress("complete", "Cache already exists", 0, 0)
		return result, nil
	}

	c.callProgress("fetching_registry", "Looking up cache registry", 0, 0)

	// Get cache registry information
	registryResp, err := c.client.CacheRegistry(ctx, c.registry)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get cache registry")
		return result, fmt.Errorf("failed to get cache registry: %w", err)
	}

	span.SetAttributes(
		attribute.String("cache.store_type", registryResp.Store),
	)

	// Validate cache store configuration
	if err := validateCacheStore(registryResp.Store, c.bucketURL); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid cache store configuration")
		return result, fmt.Errorf("invalid cache store configuration: %w", err)
	}

	c.callProgress("building_archive", "Building archive", 0, len(cacheConfig.Paths))

	// Build archive
	archiveInfo, err := archive.BuildArchive(ctx, cacheConfig.Paths, cacheConfig.Key)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to build archive")
		return result, fmt.Errorf("failed to build archive: %w", err)
	}

	// Populate archive metrics
	result.Archive = ArchiveMetrics{
		Size:             archiveInfo.Size,
		WrittenBytes:     archiveInfo.WrittenBytes,
		WrittenEntries:   archiveInfo.WrittenEntries,
		CompressionRatio: float64(archiveInfo.WrittenBytes) / float64(archiveInfo.Size),
		Sha256Sum:        archiveInfo.Sha256sum,
		Duration:         archiveInfo.Duration,
		Paths:            cacheConfig.Paths,
	}

	span.SetAttributes(
		attribute.Int64("cache.archive_size_bytes", archiveInfo.Size),
		attribute.Int64("cache.written_bytes", archiveInfo.WrittenBytes),
		attribute.Int64("cache.written_entries", archiveInfo.WrittenEntries),
		attribute.Float64("cache.compression_ratio", result.Archive.CompressionRatio),
		attribute.String("cache.sha256sum", archiveInfo.Sha256sum),
	)

	c.callProgress("creating_entry", "Creating cache entry", 0, 0)

	// Create cache entry
	createResp, err := c.client.CacheCreate(ctx, registryResp.Name, api.CacheCreateReq{
		Key:          cacheConfig.Key,
		FallbackKeys: cacheConfig.FallbackKeys,
		Compression:  c.format,
		FileSize:     int(archiveInfo.Size),
		Digest:       fmt.Sprintf("sha256:%s", archiveInfo.Sha256sum),
		Paths:        cacheConfig.Paths,
		Platform:     c.platform,
		Pipeline:     c.pipeline,
		Branch:       c.branch,
		Organization: c.organization,
		Store:        registryResp.Store,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create cache entry")
		return result, fmt.Errorf("failed to create cache entry: %w", err)
	}

	result.UploadID = createResp.UploadID

	span.SetAttributes(
		attribute.String("cache.upload_id", createResp.UploadID),
		attribute.String("cache.object_name", createResp.StoreObjectName),
	)

	c.callProgress("uploading", "Uploading cache archive", 0, int(archiveInfo.Size))

	// Upload archive
	blobStore, err := store.NewBlobStore(ctx, registryResp.Store, c.bucketURL)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create blob store")
		return result, fmt.Errorf("failed to create blob store: %w", err)
	}

	transferInfo, err := blobStore.Upload(ctx, archiveInfo.ArchivePath, createResp.StoreObjectName)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to upload cache")
		return result, fmt.Errorf("failed to upload cache: %w", err)
	}

	// Populate transfer metrics
	result.Transfer = &TransferMetrics{
		BytesTransferred: transferInfo.BytesTransferred,
		TransferSpeed:    transferInfo.TransferSpeed,
		Duration:         transferInfo.Duration,
		RequestID:        transferInfo.RequestID,
	}

	span.SetAttributes(
		attribute.Int64("cache.transfer_bytes", transferInfo.BytesTransferred),
		attribute.Float64("cache.transfer_speed_mbps", transferInfo.TransferSpeed),
		attribute.String("cache.request_id", transferInfo.RequestID),
	)

	c.callProgress("committing", "Committing cache entry", 0, 0)

	// Commit cache
	_, err = c.client.CacheCommit(ctx, c.registry, api.CacheCommitReq{
		UploadID: createResp.UploadID,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to commit cache")
		return result, fmt.Errorf("failed to commit cache: %w", err)
	}

	result.CacheCreated = true
	result.TotalDuration = time.Since(startTime)

	// Add final result attributes to span
	span.SetAttributes(
		attribute.Bool("cache.created", true),
		attribute.Int64("cache.duration_ms", result.TotalDuration.Milliseconds()),
	)
	span.SetStatus(codes.Ok, "cache saved successfully")

	c.callProgress("complete", "Cache saved successfully", 0, 0)

	return result, nil
}

// checkPathsExist validates that all paths exist on the filesystem
func checkPathsExist(paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("no paths provided")
	}

	for _, path := range paths {
		// Handle ~ expansion
		if len(path) > 0 && path[0] == '~' {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to get home directory: %w", err)
			}
			path = homeDir + path[1:]
		}

		// Check if the path exists
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return fmt.Errorf("path does not exist: %s", path)
		}
	}

	return nil
}

// validateCacheStore validates the cache store configuration
func validateCacheStore(storeType string, bucketURL string) error {
	if !store.IsValidStore(storeType) {
		return fmt.Errorf("unsupported cache store: %s", storeType)
	}

	switch storeType {
	case store.LocalS3Store:
		if bucketURL == "" {
			return fmt.Errorf("bucket URL is required for S3 store")
		}
		// Note: We allow both s3:// and file:// for S3 store (file:// is for local testing)
	case store.LocalHostedAgents:
		if bucketURL != "" {
			return fmt.Errorf("NSC store should not have bucket URL set, got: %s", bucketURL)
		}
	}

	return nil
}
