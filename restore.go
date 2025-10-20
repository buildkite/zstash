package zstash

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/buildkite/zstash/api"
	"github.com/buildkite/zstash/archive"
	"github.com/buildkite/zstash/store"
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
// Returns RestoreResult with detailed metrics. Check RestoreResult.Error to
// determine if the operation succeeded. Returns a non-nil error only for
// critical failures.
//
// Use RestoreResult.CacheHit to check if the exact key matched, and
// RestoreResult.FallbackUsed to check if a fallback key was used.
//
// Example:
//
//	result, err := cacheClient.Restore(ctx, "node_modules")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if result.Error != nil {
//	    log.Printf("Cache restore failed: %v", result.Error)
//	} else if !result.CacheRestored {
//	    log.Printf("Cache miss for key: %s", result.Key)
//	} else if result.FallbackUsed {
//	    log.Printf("Restored from fallback key: %s", result.Key)
//	} else {
//	    log.Printf("Cache hit: %s (%.2f MB)", result.Key, float64(result.Archive.Size)/(1024*1024))
//	}
func (c *Cache) Restore(ctx context.Context, cacheID string) (RestoreResult, error) {
	startTime := time.Now()
	result := RestoreResult{
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

	c.callProgress("checking_exists", "Checking if cache exists", 0, 0)

	// Check if cache exists
	retrieveResp, exists, err := c.client.CacheRetrieve(ctx, result.Registry, api.CacheRetrieveReq{
		Key:          cacheConfig.Key,
		Branch:       c.branch,
		FallbackKeys: strings.Join(cacheConfig.FallbackKeys, ","),
	})
	if err != nil {
		result.Error = fmt.Errorf("failed to retrieve cache: %w", err)
		return result, result.Error
	}

	if !exists {
		// Cache miss
		result.CacheHit = false
		result.CacheRestored = false
		result.TotalDuration = time.Since(startTime)
		c.callProgress("complete", "Cache miss", 0, 0)
		return result, nil
	}

	// Cache found (either exact match or fallback)
	result.Key = retrieveResp.Key
	result.FallbackUsed = retrieveResp.Fallback
	result.CacheHit = !retrieveResp.Fallback
	result.ExpiresAt = retrieveResp.ExpiresAt

	c.callProgress("downloading", "Downloading cache archive", 0, 0)

	// Download cache
	tmpDir, archiveFile, transferInfo, err := c.downloadCache(ctx, retrieveResp, c.bucketURL)
	if err != nil {
		result.Error = fmt.Errorf("failed to download cache: %w", err)
		return result, result.Error
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
		result.Error = fmt.Errorf("failed to extract cache: %w", err)
		return result, result.Error
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
	c.callProgress("complete", "Cache restored successfully", 0, 0)

	return result, nil
}

// RestoreAll restores all caches configured in this cache client concurrently.
//
// The function launches a goroutine for each cache and restores them in parallel.
// This is more efficient than calling Restore sequentially for multiple caches.
//
// Individual cache failures are captured in each RestoreResult.Error field.
// Cache misses (no matching key or fallbacks) are not considered errors - check
// RestoreResult.CacheRestored to determine if a cache was actually restored.
//
// The operation respects context cancellation. If ctx is cancelled, all
// in-progress restore operations will be stopped.
//
// Returns a map where keys are cache IDs and values are RestoreResults.
// Always check each RestoreResult.Error to determine if that specific cache
// operation succeeded.
//
// Example:
//
//	results, err := cacheClient.RestoreAll(ctx)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	hits, misses := 0, 0
//	for cacheID, result := range results {
//	    if result.Error != nil {
//	        log.Printf("Failed to restore %s: %v", cacheID, result.Error)
//	    } else if result.CacheRestored {
//	        hits++
//	        log.Printf("Restored %s: %s", cacheID, result.Key)
//	    } else {
//	        misses++
//	        log.Printf("Cache miss %s", cacheID)
//	    }
//	}
//	log.Printf("Restore complete: %d hits, %d misses", hits, misses)
func (c *Cache) RestoreAll(ctx context.Context) (map[string]RestoreResult, error) {
	results := make(map[string]RestoreResult)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, cacheConfig := range c.caches {
		wg.Add(1)
		go func(cacheID string) {
			defer wg.Done()

			result, err := c.Restore(ctx, cacheID)
			if err != nil {
				// Critical failure
				result = RestoreResult{
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

// downloadCache downloads a cache archive from storage
func (c *Cache) downloadCache(ctx context.Context, retrieveResp api.CacheRetrieveResp, bucketURL string) (tmpDir string, archiveFile string, transferInfo *store.TransferInfo, err error) {
	// Create blob store
	blobStore, err := store.NewBlobStore(ctx, retrieveResp.Store, bucketURL)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to create blob store: %w", err)
	}

	// Create temporary directory
	tmpDir, err = os.MkdirTemp("", "zstash-restore")
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	archiveFile = filepath.Join(tmpDir, retrieveResp.StoreObjectName)

	// Download archive
	transferInfo, err = blobStore.Download(ctx, retrieveResp.StoreObjectName, archiveFile)
	if err != nil {
		// Clean up temporary directory on failure
		_ = os.RemoveAll(tmpDir)
		return "", "", nil, fmt.Errorf("failed to download cache: %w", err)
	}

	return tmpDir, archiveFile, transferInfo, nil
}

// extractCache extracts files from a cache archive
func (c *Cache) extractCache(ctx context.Context, archiveFile string, archiveSize int64, paths []string) (*archive.ArchiveInfo, error) {
	// Open archive file
	archiveFileHandle, err := os.Open(archiveFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open archive file: %w", err)
	}
	defer archiveFileHandle.Close()

	// Extract files
	archiveInfo, err := archive.ExtractFiles(ctx, archiveFileHandle, archiveSize, paths)
	if err != nil {
		return nil, fmt.Errorf("failed to extract archive: %w", err)
	}

	return archiveInfo, nil
}
