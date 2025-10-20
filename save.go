package zstash

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/buildkite/zstash/api"
	"github.com/buildkite/zstash/archive"
	"github.com/buildkite/zstash/store"
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
// Returns SaveResult with detailed metrics. Check SaveResult.Error to determine
// if the operation succeeded. Returns a non-nil error only for critical failures.
//
// Example:
//
//	result, err := cacheClient.Save(ctx, "node_modules")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if result.Error != nil {
//	    log.Printf("Cache save failed: %v", result.Error)
//	} else if !result.CacheCreated {
//	    log.Printf("Cache already exists for key: %s", result.Key)
//	} else {
//	    log.Printf("Cache saved: %s (%.2f MB)", result.Key, float64(result.Archive.Size)/(1024*1024))
//	}
func (c *Cache) Save(ctx context.Context, cacheID string) (SaveResult, error) {
	startTime := time.Now()
	result := SaveResult{
		Registry: "~", // default registry
	}

	// Find the cache configuration
	cacheConfig, err := c.findCache(cacheID)
	if err != nil {
		result.Error = err
		return result, err
	}

	// Set registry (default to "~" if not specified)
	if cacheConfig.Registry != "" {
		result.Registry = cacheConfig.Registry
	}

	result.Key = cacheConfig.Key

	c.callProgress("validating", "Validating cache configuration", 0, 0)

	// Validate cache paths exist
	if err := checkPathsExist(cacheConfig.Paths); err != nil {
		result.Error = fmt.Errorf("invalid cache paths: %w", err)
		return result, result.Error
	}

	c.callProgress("checking_exists", "Checking if cache already exists", 0, 0)

	// Check if cache already exists
	_, exists, err := c.client.CachePeekExists(ctx, result.Registry, api.CachePeekReq{
		Key:    cacheConfig.Key,
		Branch: c.branch,
	})
	if err != nil {
		result.Error = fmt.Errorf("failed to check cache existence: %w", err)
		return result, result.Error
	}

	if exists {
		// Cache already exists, no need to upload
		result.CacheCreated = false
		result.TotalDuration = time.Since(startTime)
		c.callProgress("complete", "Cache already exists", 0, 0)
		return result, nil
	}

	c.callProgress("fetching_registry", "Looking up cache registry", 0, 0)

	// Get cache registry information
	registryResp, err := c.client.CacheRegistry(ctx, result.Registry)
	if err != nil {
		result.Error = fmt.Errorf("failed to get cache registry: %w", err)
		return result, result.Error
	}

	// Validate cache store configuration
	if err := validateCacheStore(registryResp.Store, c.bucketURL); err != nil {
		result.Error = fmt.Errorf("invalid cache store configuration: %w", err)
		return result, result.Error
	}

	c.callProgress("building_archive", "Building archive", 0, len(cacheConfig.Paths))

	// Build archive
	archiveInfo, err := archive.BuildArchive(ctx, cacheConfig.Paths, cacheConfig.Key)
	if err != nil {
		result.Error = fmt.Errorf("failed to build archive: %w", err)
		return result, result.Error
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
		result.Error = fmt.Errorf("failed to create cache entry: %w", err)
		return result, result.Error
	}

	result.UploadID = createResp.UploadID

	c.callProgress("uploading", "Uploading cache archive", 0, int(archiveInfo.Size))

	// Upload archive
	blobStore, err := store.NewBlobStore(ctx, registryResp.Store, c.bucketURL)
	if err != nil {
		result.Error = fmt.Errorf("failed to create blob store: %w", err)
		return result, result.Error
	}

	transferInfo, err := blobStore.Upload(ctx, archiveInfo.ArchivePath, createResp.StoreObjectName)
	if err != nil {
		result.Error = fmt.Errorf("failed to upload cache: %w", err)
		return result, result.Error
	}

	// Populate transfer metrics
	result.Transfer = &TransferMetrics{
		BytesTransferred: transferInfo.BytesTransferred,
		TransferSpeed:    transferInfo.TransferSpeed,
		Duration:         transferInfo.Duration,
		RequestID:        transferInfo.RequestID,
	}

	c.callProgress("committing", "Committing cache entry", 0, 0)

	// Commit cache
	_, err = c.client.CacheCommit(ctx, result.Registry, api.CacheCommitReq{
		UploadID: createResp.UploadID,
	})
	if err != nil {
		result.Error = fmt.Errorf("failed to commit cache: %w", err)
		return result, result.Error
	}

	result.CacheCreated = true
	result.TotalDuration = time.Since(startTime)
	c.callProgress("complete", "Cache saved successfully", 0, 0)

	return result, nil
}

// SaveAll saves all caches configured in this cache client concurrently.
//
// The function launches a goroutine for each cache and saves them in parallel.
// This is more efficient than calling Save sequentially for multiple caches.
//
// Individual cache failures are captured in each SaveResult.Error field.
// The function returns a map of cache ID to SaveResult for all caches.
//
// The operation respects context cancellation. If ctx is cancelled, all
// in-progress save operations will be stopped.
//
// Returns a map where keys are cache IDs and values are SaveResults.
// Always check each SaveResult.Error to determine if that specific cache
// operation succeeded.
//
// Example:
//
//	results, err := cacheClient.SaveAll(ctx)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	for cacheID, result := range results {
//	    if result.Error != nil {
//	        log.Printf("Failed to save %s: %v", cacheID, result.Error)
//	    } else if result.CacheCreated {
//	        log.Printf("Saved %s: %s", cacheID, result.Key)
//	    } else {
//	        log.Printf("Already exists %s: %s", cacheID, result.Key)
//	    }
//	}
func (c *Cache) SaveAll(ctx context.Context) (map[string]SaveResult, error) {
	results := make(map[string]SaveResult)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, cacheConfig := range c.caches {
		wg.Add(1)
		go func(cacheID string) {
			defer wg.Done()

			result, err := c.Save(ctx, cacheID)
			if err != nil {
				// Critical failure
				result = SaveResult{
					Error: err,
				}
			}

			mu.Lock()
			results[cacheID] = result
			mu.Unlock()
		}(cacheConfig.ID)
	}

	wg.Wait()
	return results, nil
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
