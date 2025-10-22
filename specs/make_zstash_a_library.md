# Refactoring Plan: Convert zstash to Library

## Overview

Transform zstash from a standalone CLI tool into a reusable Go library by:
1. Moving `main.go` to `cmd/zstash/main.go` (CLI binary)
2. Moving `internal/` packages to root-level packages
3. Creating clean public API at module root: `github.com/buildkite/zstash`

## Phase 1: Restructure Packages

### Move internal packages to root level:

1. **`cache/`** (from `internal/cache`)
   - Public API: `Cache` struct, `Validate()` method
   - No changes needed

2. **`store/`** (from `internal/store`)
   - Public API: `Blob` interface, `TransferInfo`, `NewBlobStore()`, store constants
   - Keep unexported: helper functions

3. **`archive/`** (from `internal/archive`)
   - Public API: `ArchiveInfo`, `BuildArchive()`, `ExtractFiles()`
   - Keep unexported: `checksumSHA256`, helpers

4. **`api/`** (from `internal/api`)
   - Public API: `Client`, all request/response types, `NewClient()`

5. **`configuration/`** (from `internal/configuration`)
   - Public API: `ExpandCacheConfiguration(caches, env)` - now accepts environment map
   - Public API: `ExpandCacheConfigurationFromOS(caches)` - convenience for CLI usage
   - Keep unexported: template helpers

### Keep in internal/ (not needed by agent):

- `internal/trace` - agent has own tracing
- `internal/console` - agent has own UI
- `internal/key` - used by configuration, doesn't need export

### New structure:

- `cmd/zstash/main.go` - CLI binary
- `internal/commands/` - CLI command implementations

## Phase 2: Create Root-Level Library API

### Cache-Based API Design

Create **`zstash.go`** at module root with a cache-oriented API:

```go
package zstash

import (
    "context"
    "fmt"
    "runtime"
    "time"

    "github.com/buildkite/zstash/api"
    "github.com/buildkite/zstash/cache"
    "github.com/buildkite/zstash/configuration"
)

// Cache provides cache save and restore operations.
// Create a cache client once with your configuration, then use it for multiple operations.
type Cache struct {
    // internal fields...
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
//
// This function:
//   1. Validates the configuration
//   2. Expands cache template variables using cfg.Env (if provided)
//   3. Validates all cache configurations
//   4. Returns a ready-to-use cache client
//
// Returns an error if configuration is invalid or cache validation fails.
func NewCache(cfg Config) (*Cache, error)

// Save saves a cache to storage by ID.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts
//   - cacheID: The ID of the cache to save (must exist in cache configuration)
//
// Returns detailed results about the save operation.
func (c *Cache) Save(ctx context.Context, cacheID string) (SaveResult, error)

// Restore restores a cache from storage by ID.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts
//   - cacheID: The ID of the cache to restore (must exist in cache configuration)
//
// Returns detailed results about the restore operation.
func (c *Cache) Restore(ctx context.Context, cacheID string) (RestoreResult, error)

// ListCaches returns all cache configurations managed by this cache client.
// These caches have already been expanded and validated.
func (c *Cache) ListCaches() []cache.Cache

// GetCache returns a specific cache configuration by ID.
// Returns an error if the cache ID is not found.
func (c *Cache) GetCache(id string) (cache.Cache, error)
```

### Result Types

Create **`types.go`** at module root:

```go
package zstash

import "time"

// SaveResult contains detailed information about a cache save operation
type SaveResult struct {
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
```

### Error Handling

Create **`errors.go`** at module root:

```go
package zstash

import "errors"

// Sentinel errors for common scenarios
var (
    // ErrCacheNotFound is returned when a cache ID doesn't exist in the service
    ErrCacheNotFound = errors.New("cache not found")

    // ErrInvalidConfiguration is returned when configuration validation fails
    ErrInvalidConfiguration = errors.New("invalid configuration")
)
```

### Cache Client Behavior

**Validation in NewCache():**
- **Required**: `Config.Client` must not be nil
- **Defaults**: `Format` → "zip", `Platform` → runtime.GOOS/GOARCH
- **Optional**: `OnProgress` can be nil, `Env` can be nil
- **Cache expansion**: If `Env` provided, expands templates; validates all caches
- **Registry default**: Empty `cache.Registry` defaults to "~" during operations

**Thread Safety:**
- Cache is safe for concurrent use
- Multiple goroutines can call `Save()`/`Restore()` simultaneously
- Progress callbacks must be thread-safe (caller's responsibility)

**Context Cancellation:**
- Operations stop immediately when context is cancelled
- Temporary files are cleaned up
- Progress callback called with stage="cancelled"
- Returns `context.Canceled` error

**Progress Callback Stages:**

*Save operations:*
1. `"validating"` - Validating cache configuration
2. `"checking_exists"` - Checking if cache exists
3. `"fetching_registry"` - Looking up cache registry
4. `"building_archive"` - Building archive (with file count progress)
5. `"creating_entry"` - Creating cache entry
6. `"uploading"` - Uploading cache (with bytes progress)
7. `"committing"` - Committing cache entry
8. `"complete"` - Operation finished

*Restore operations:*
1. `"validating"` - Validating cache configuration
2. `"checking_exists"` - Checking if cache exists
3. `"downloading"` - Downloading cache (with bytes progress)
4. `"extracting"` - Extracting files (with file count progress)
5. `"complete"` - Operation finished

## Phase 3: Extract Core Logic

Create **`cache.go`**, **`save.go`**, and **`restore.go`** at root:

**`cache.go`** - Cache client implementation:
- Implements `NewCache()` with validation and cache expansion
- Implements `ListCaches()` and `GetCache()` helper methods
- Handles defaults (format, platform, registry)
- Thread-safe implementation

**`save.go`** - Save implementation:
- Extract logic from `internal/commands/save.go`
- Implement `Cache.Save()`
- Remove CLI-specific code (printer, stdout writes, table formatting)
- Keep core workflow: validate → peek → registry → archive → upload → commit
- Add progress callbacks at each stage
- Return detailed `SaveResult` with metrics

**`restore.go`** - Restore implementation:
- Extract logic from `internal/commands/restore.go`
- Implement `Cache.Restore()`
- Remove CLI-specific code (printer, stdout writes, table formatting)
- Keep core workflow: validate → retrieve → download → extract
- Add progress callbacks at each stage
- Return detailed `RestoreResult` with metrics

**Key changes from command implementations:**
- Replace `globals.Printer` calls with progress callbacks
- Replace `fmt.Println()` stdout writes with result fields
- Remove `lipgloss` table formatting
- Return structured results instead of printing
- Handle context cancellation with cleanup
- Protect progress callbacks from panics

## Phase 4: Update CLI to Use Library

Update **`cmd/zstash/main.go`**:
- Import `github.com/buildkite/zstash`
- Create `zstash.Cache` during initialization
- Pass `OnProgress` callback that writes to CLI printer
- Convert `os.Environ()` to map for `Config.Env`

Update **`internal/commands/`**:
- Commands become thin wrappers around cache client methods
- `SaveCmd.Run()` iterates through cache IDs and calls `cacheClient.Save()` for each
- `RestoreCmd.Run()` iterates through cache IDs and calls `cacheClient.Restore()` for each
- Transform `SaveResult`/`RestoreResult` into CLI output:
  - Use `printer` for status messages
  - Use `lipgloss` tables for result summaries
  - Write `"true"/"false"` to stdout based on results
- Keep all CLI-specific formatting and presentation logic

**Example CLI command implementation:**

```go
// internal/commands/save.go (refactored)
package commands

import (
    "context"
    "fmt"

    "github.com/buildkite/zstash"
    "github.com/charmbracelet/lipgloss/table"
    "github.com/dustin/go-humanize"
)

type SaveCmd struct {
    Ids []string `flag:"ids" help:"List of comma delimited cache IDs to save"`
}

func (cmd *SaveCmd) Run(ctx context.Context, globals *Globals) error {
    // Cache client is created in main.go and passed via Globals
    cacheClient := globals.CacheClient

    // Determine which caches to save
    cacheIDs := cmd.Ids
    if len(cacheIDs) == 0 {
        // Save all caches configured in the client
        caches := cacheClient.ListCaches()
        for _, cache := range caches {
            cacheIDs = append(cacheIDs, cache.ID)
        }
    }

    // Save each cache
    for _, cacheID := range cacheIDs {
        result, err := cacheClient.Save(ctx, cacheID)
        if err != nil {
            globals.Printer.Error("❌", "Cache save failed for ID %s: %s", cacheID, err)
            continue
        }

        if !result.CacheCreated {
            globals.Printer.Success("✅", "Cache already exists for key: %s", result.Key)
            fmt.Println("true") // stdout
            continue
        }

        // Print success summary table
        t := table.New().
            Border(lipgloss.NormalBorder()).
            Row("Key", result.Key).
            Row("Archive Size", humanize.Bytes(uint64(result.Archive.Size))).
            Row("Written Bytes", humanize.Bytes(uint64(result.Archive.WrittenBytes))).
            Row("Written Entries", fmt.Sprintf("%d", result.Archive.WrittenEntries)).
            Row("Compression Ratio", fmt.Sprintf("%.2f", result.Archive.CompressionRatio)).
            Row("Transfer Speed", fmt.Sprintf("%.2fMB/s", result.Transfer.TransferSpeed))

        globals.Printer.Info("📊", "Cache save summary:\n%s", t.Render())
        fmt.Println("true") // stdout
    }

    return nil
}
```

## Final File Structure

```
zstash/
├── cmd/
│   └── zstash/
│       └── main.go           # CLI binary
├── internal/
│   ├── commands/             # CLI command handlers
│   ├── console/              # UI/printing (CLI only)
│   ├── trace/                # Tracing (CLI only)
│   └── key/                  # Key templating (used by config)
├── api/                      # API client (PUBLIC)
├── archive/                  # Archive operations (PUBLIC)
├── cache/                    # Cache types (PUBLIC)
├── configuration/            # Config expansion (PUBLIC)
├── store/                    # Blob storage (PUBLIC)
├── zstash.go                 # Types, errors, and API (PUBLIC)
├── cache.go                  # Cache client implementation (PUBLIC)
├── save.go                   # Save implementation (PUBLIC)
├── restore.go                # Restore implementation (PUBLIC)
├── go.mod
└── README.md
```

## Agent Integration Example

In buildkite-agent:

### Agent Initialization

```go
package agent

import (
    "context"
    "fmt"

    "github.com/buildkite/zstash"
    "github.com/buildkite/zstash/api"
)

type Agent struct {
    cacheClient *zstash.Cache
    logger      Logger
    // ... other fields
}

func (a *Agent) Initialize() error {
    // Create API client
    client := api.NewClient(
        context.Background(),
        a.version,
        a.config.Endpoint,
        a.config.Token,
    )

    // Create cache client
    cacheClient, err := zstash.NewCache(zstash.Config{
        Client:       client,
        BucketURL:    a.config.CacheBucketURL,
        Branch:       a.env.Branch,
        Pipeline:     a.env.Pipeline,
        Organization: a.env.Organization,
        Format:       "zip",
        Env:          a.env.ToMap(), // convert agent env to map
        Caches:       a.config.Caches,
        OnProgress:   a.logCacheProgress,
    })
    if err != nil {
        return fmt.Errorf("failed to initialize cache client: %w", err)
    }

    a.cacheClient = cacheClient

    // List available caches for debugging
    caches := cacheClient.ListCaches()
    a.logger.Debug("Initialized cache client",
        "cache_count", len(caches))

    return nil
}

func (a *Agent) logCacheProgress(stage string, message string, current int, total int) {
    fields := []any{
        "stage", stage,
        "message", message,
    }

    if total > 0 {
        percentage := float64(current) / float64(total) * 100
        fields = append(fields,
            "percentage", fmt.Sprintf("%.1f%%", percentage))
    }

    a.logger.Debug("Cache operation progress", fields...)
}
```

### Cache Restore

```go
func (a *Agent) CacheRestore(ctx context.Context, cacheID string) (bool, error) {
    result, err := a.cacheClient.Restore(ctx, cacheID)
    if err != nil {
        return false, fmt.Errorf("cache restore failed: %w", err)
    }

    if !result.CacheRestored {
        a.logger.Warn("Cache miss",
            "cache_id", cacheID,
            "key", result.Key)
        return false, nil
    }

    a.logger.Info("Cache restored successfully",
        "cache_id", cacheID,
        "key", result.Key,
        "cache_hit", result.CacheHit,
        "fallback_used", result.FallbackUsed,
        "size_mb", float64(result.Archive.Size)/(1024*1024),
        "entries", result.Archive.WrittenEntries,
        "transfer_speed_mbps", result.Transfer.TransferSpeed,
        "duration_ms", result.TotalDuration.Milliseconds())

    return result.CacheHit, nil
}
```

### Cache Save

```go
func (a *Agent) CacheSave(ctx context.Context, cacheID string) error {
    result, err := a.cacheClient.Save(ctx, cacheID)
    if err != nil {
        return fmt.Errorf("cache save failed: %w", err)
    }

    a.logger.Info("Cache saved successfully",
        "cache_id", cacheID,
        "key", result.Key,
        "created", result.CacheCreated,
        "size_mb", float64(result.Archive.Size)/(1024*1024),
        "entries", result.Archive.WrittenEntries,
        "compression_ratio", result.Archive.CompressionRatio)

    if result.Transfer != nil {
        a.logger.Debug("Cache upload metrics",
            "cache_id", cacheID,
            "transfer_speed_mbps", result.Transfer.TransferSpeed,
            "duration_ms", result.Transfer.Duration.Milliseconds())
    } else {
        a.logger.Info("Cache already existed, skipped upload",
            "cache_id", cacheID)
    }

    return nil
}
```

### Batch Operations

If you need to restore multiple caches, you can iterate through them:

```go
func (a *Agent) CacheRestoreAll(ctx context.Context) error {
    // Get all configured caches
    caches := a.cacheClient.ListCaches()

    var (
        hits     int
        misses   int
        failures []string
    )

    for _, cache := range caches {
        result, err := a.cacheClient.Restore(ctx, cache.ID)
        if err != nil {
            a.logger.Error("Cache restore failed",
                "cache_id", cache.ID,
                "error", err)
            failures = append(failures, cache.ID)
            continue
        }

        if result.CacheRestored {
            if result.CacheHit {
                hits++
            } else {
                misses++
            }
        } else {
            misses++
        }
    }

    a.logger.Info("Cache restore completed",
        "total", len(caches),
        "hits", hits,
        "misses", misses,
        "failures", len(failures))

    if len(failures) > 0 {
        return fmt.Errorf("failed to restore %d caches", len(failures))
    }

    return nil
}
```

## Estimated Changes

- **1 directory created** (`cmd/zstash/`)
- **5 directories moved** (internal/* → root): `api/`, `archive/`, `cache/`, `configuration/`, `store/`
- **6 new files at root**: `zstash.go`, `service.go`, `save.go`, `restore.go`, `types.go`, `errors.go`
- **~800 lines** extracted from commands into library
- **All tests preserved** and moved with packages
- **Update `configuration/` package** to accept environment map parameter
- **Refactor CLI commands** to use service (~200 lines simplified)

## Benefits

### For Agent Integration
- **Clean import**: `import "github.com/buildkite/zstash"`
- **Stateful client**: Initialize once, use many times
- **Rich metrics**: Detailed results for logging and observability
- **Progress tracking**: Optional callbacks for UI updates
- **Type-safe**: No magic booleans, clear result structures
- **Flexible**: Support for single cache or batch operations
- **Early validation**: Catch configuration errors at cache client creation

### For CLI
- **Simplified commands**: CLI becomes thin wrapper around library
- **Shared logic**: No duplicate code between CLI and library
- **Better testability**: Core logic tested independently of CLI
- **Maintained compatibility**: CLI behavior unchanged for users

### General
- **Standard Go layout**: Follows best practices for Go libraries
- **Concurrent-safe**: Cache client can be used by multiple goroutines
- **Context-aware**: Proper cancellation and timeout support
- **Extensible**: Easy to add new features without breaking changes
- **Well-documented**: Clear GoDoc comments and examples
