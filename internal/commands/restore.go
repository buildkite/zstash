package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/buildkite/zstash/internal/api"
	"github.com/buildkite/zstash/internal/archive"
	"github.com/buildkite/zstash/internal/cache"
	"github.com/buildkite/zstash/internal/configuration"
	"github.com/buildkite/zstash/internal/store"
	"github.com/buildkite/zstash/internal/trace"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/dustin/go-humanize"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	CacheRestoreHit  = "true"
	CacheRestoreMiss = "false"
)

type RestoreCmd struct {
	Ids []string `flag:"ids" help:"List of comma delimited cache IDs to restore, defaults to all." env:"BUILDKITE_CACHE_IDS"`
}

func (cmd *RestoreCmd) Run(ctx context.Context, globals *Globals) error {
	ctx, span := trace.Start(ctx, "RestoreCmdRun")
	defer span.End()

	log.Info().Str("version", globals.Version).Msg("Running RestoreCmd")

	// Augment `cli.Caches` with template values.
	caches, err := configuration.ExpandCacheConfiguration(globals.Caches)
	if err != nil {
		return fmt.Errorf("failed to load cache configuration: %w", err)
	}

	for _, cache := range caches {
		if len(cmd.Ids) > 0 && !slices.Contains(cmd.Ids, cache.ID) {
			log.Debug().Str("id", cache.ID).Msg("Skipping cache restore for ID")
			continue
		}

		if err := cache.Validate(); err != nil {
			return fmt.Errorf("cache validation failed for ID %s: %w", cache.ID, err)
		}

		if err := cmd.restoreCache(ctx, cache, globals); err != nil {
			return err
		}
	}

	return nil
}

func (cmd *RestoreCmd) restoreCache(ctx context.Context, cache cache.Cache, globals *Globals) error {
	ctx, span := trace.Start(ctx, "restoreCache")
	defer span.End()

	span.SetAttributes(
		attribute.String("key", cache.Key),
		attribute.StringSlice("paths", cache.Paths),
		attribute.StringSlice("fallback_keys", cache.FallbackKeys),
	)

	// Phase 1: Validate and prepare data
	data, err := cmd.validateAndPrepare(ctx, span, cache)
	if err != nil {
		return trace.NewError(span, "failed to validate and prepare cache: %w", err)
	}

	if cache.Registry == "" {
		cache.Registry = "~" // default to "~" if not set
	}

	globals.Printer.Info("♻️", "Starting restore for Registry: %s ID: %s", cache.Registry, cache.ID)

	// Phase 2: Check if cache exists
	cacheResult, err := cmd.checkCacheExists(ctx, data, globals.Client, cache.Registry, globals.Common.Branch)
	if err != nil {
		return trace.NewError(span, "failed to check cache existence: %w", err)
	}

	if !cacheResult.exists {
		globals.Printer.Warn("💨", "Cache miss for key: %s", data.cacheKey)
		fmt.Println(CacheRestoreMiss)

		return nil
	}

	if cacheResult.fallback {
		globals.Printer.Warn("⚠️", "Using fallback cache for key: %s", cacheResult.cacheKey)
	} else {
		globals.Printer.Info("✅", "Cache hit for key: %s", cacheResult.cacheKey)
	}

	globals.Printer.Info("⬇️", "Downloading cache for key: %s", cacheResult.cacheKey)

	// Phase 3: Download cache
	downloadResult, err := cmd.downloadCache(ctx, cacheResult, globals.Common.BucketURL)
	if err != nil {
		return trace.NewError(span, "failed to download cache: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(downloadResult.tmpDir)
	}()

	globals.Printer.Success("✅", "Download completed: %s at %.2fMB/s",
		humanize.Bytes(Int64ToUint64(downloadResult.transferInfo.BytesTransferred)),
		downloadResult.transferInfo.TransferSpeed)

	// Phase 4: Extract files
	extractionResult, err := cmd.extractFiles(ctx, span, downloadResult, data.paths)
	if err != nil {
		return trace.NewError(span, "failed to extract files from cache: %w", err)
	}

	globals.Printer.Success("🗜️", "Extracted %d files from cache archive for paths: %v", extractionResult.archiveInfo.WrittenEntries, extractionResult.paths)

	// Phase 5: Generate summary and output
	t := table.New().
		Border(lipgloss.NormalBorder()).
		Row("Key", cacheResult.cacheKey).
		Row("Size", humanize.Bytes(Int64ToUint64(extractionResult.archiveInfo.Size))).
		Row("Written Bytes", humanize.Bytes(Int64ToUint64(extractionResult.archiveInfo.WrittenBytes))).
		Row("Written Entries", fmt.Sprintf("%d", extractionResult.archiveInfo.WrittenEntries)).
		Row("Compression Ratio", fmt.Sprintf("%.2f", compressionRatio(extractionResult.archiveInfo))).
		Row("Transfer Speed", fmt.Sprintf("%.2fMB/s", downloadResult.transferInfo.TransferSpeed)).
		Row("Duration", extractionResult.archiveInfo.Duration.String()).
		Row("Paths", strings.Join(extractionResult.paths, ", "))

	globals.Printer.Info("📊", "Cache restore summary:\n%s", t.Render())

	if cacheResult.fallback {
		fmt.Println(CacheRestoreMiss) // write to stdout
		return nil
	}

	// If we reach here, it means the restore was successful and we indicate that we HIT
	fmt.Println(CacheRestoreHit) // write to stdout

	return nil
}

type restoreData struct {
	paths             []string
	cacheKey          string
	fallbackCacheKeys []string
}

type cacheExistenceResult struct {
	exists          bool
	cacheKey        string
	storeObjectName string
	store           string
	fallback        bool
	expiresAt       time.Time
}

type downloadResult struct {
	archiveFile  string
	transferInfo *store.TransferInfo
	tmpDir       string
}

type extractionResult struct {
	archiveInfo *archive.ArchiveInfo
	paths       []string
}

func (cmd *RestoreCmd) validateAndPrepare(ctx context.Context, span oteltrace.Span, cache cache.Cache) (*restoreData, error) {
	return &restoreData{
		paths:             cache.Paths,
		cacheKey:          cache.Key,
		fallbackCacheKeys: cache.FallbackKeys,
	}, nil
}

func (cmd *RestoreCmd) checkCacheExists(ctx context.Context, data *restoreData, client api.Client, registry, branch string) (*cacheExistenceResult, error) {
	log.Info().
		Str("key", data.cacheKey).
		Strs("fallback_keys", data.fallbackCacheKeys).
		Msg("calling cache retrieve")

	retrieveResp, exists, err := client.CacheRetrieve(ctx, registry, api.CacheRetrieveReq{
		Key:          data.cacheKey,
		Branch:       branch,
		FallbackKeys: strings.Join(data.fallbackCacheKeys, ","),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve cache: %w", err)
	}

	if !exists {
		return &cacheExistenceResult{exists: false, cacheKey: data.cacheKey}, nil
	}

	return &cacheExistenceResult{
		exists:          exists,
		cacheKey:        retrieveResp.Key,
		storeObjectName: retrieveResp.StoreObjectName,
		fallback:        retrieveResp.Fallback,
		expiresAt:       retrieveResp.ExpiresAt,
		store:           retrieveResp.Store,
	}, nil
}

func (cmd *RestoreCmd) downloadCache(ctx context.Context, cacheResult *cacheExistenceResult, bucketURL string) (*downloadResult, error) {
	log.Debug().
		Str("bucket_url", bucketURL).
		Str("store", cacheResult.store).
		Msg("restoring cache")

	var (
		blobs store.Blob
		err   error
	)

	blobs, err = store.NewBlobStore(ctx, cacheResult.store, bucketURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create blob store: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "zstash-restore")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	archiveFile := filepath.Join(tmpDir, cacheResult.storeObjectName)

	transferInfo, err := blobs.Download(ctx, cacheResult.storeObjectName, archiveFile)
	if err != nil {
		// Clean up temporary directory if download fails
		if err := os.RemoveAll(tmpDir); err != nil {
			log.Error().Err(err).Str("tmpDir", tmpDir).Msg("failed to clean up temporary directory")
		}
		return nil, fmt.Errorf("failed to download cache: %w", err)
	}

	log.Debug().
		Int64("size", transferInfo.BytesTransferred).
		Str("transfer_speed", fmt.Sprintf("%.2fMB/s", transferInfo.TransferSpeed)).
		Str("request_id", transferInfo.RequestID).
		Str("store_object_name", cacheResult.storeObjectName).
		Dur("duration_ms", transferInfo.Duration).
		Msg("cache downloaded")

	return &downloadResult{
		archiveFile:  archiveFile,
		transferInfo: transferInfo,
		tmpDir:       tmpDir,
	}, nil
}

func (cmd *RestoreCmd) extractFiles(ctx context.Context, span oteltrace.Span, download *downloadResult, paths []string) (*extractionResult, error) {
	archiveFileHandle, err := os.Open(download.archiveFile)
	if err != nil {
		return nil, trace.NewError(span, "failed to open archive file: %w", err)
	}
	defer archiveFileHandle.Close()

	log.Info().Strs("paths", paths).Msg("extracting files")

	archiveInfo, err := archive.ExtractFiles(ctx, archiveFileHandle, download.transferInfo.BytesTransferred, paths)
	if err != nil {
		return nil, trace.NewError(span, "failed to extract archive: %w", err)
	}

	log.Debug().
		Int64("size", archiveInfo.Size).
		Dur("duration_ms", archiveInfo.Duration).
		Int64("written_bytes", archiveInfo.WrittenBytes).
		Int64("written_entries", archiveInfo.WrittenEntries).
		Float64("compression_ratio", compressionRatio(archiveInfo)).
		Msg("archive extracted")

	return &extractionResult{
		archiveInfo: archiveInfo,
		paths:       paths,
	}, nil
}
