package commands

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/buildkite/zstash/internal/api"
	"github.com/buildkite/zstash/internal/archive"
	"github.com/buildkite/zstash/internal/console"
	"github.com/buildkite/zstash/internal/key"
	"github.com/buildkite/zstash/internal/store"
	"github.com/buildkite/zstash/internal/trace"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/dustin/go-humanize"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type SaveCmd struct {
	ID []string `flag:"id" help:"List of comma delimited cache IDs to save, defaults to all." env:"BUILDKITE_CACHE_IDS"`
}

func (cmd *SaveCmd) Run(ctx context.Context, globals *Globals) error {
	ctx, span := trace.Start(ctx, "SaveCmdRun")
	defer span.End()

	log.Info().Str("version", globals.Version).Msg("Running SaveCmd")

	for _, cache := range globals.Caches {
		if len(cmd.ID) > 0 && !slices.Contains(cmd.ID, cache.ID) {
			log.Debug().Str("id", cache.ID).Msg("Skipping cache save for ID")
			continue
		}

		if err := cmd.saveCache(ctx, cache, globals.Client, globals.Printer, globals.Common); err != nil {
			return err
		}
	}

	return nil
}

func (cmd *SaveCmd) saveCache(ctx context.Context, cache Cache, client api.Client, printer *console.Printer, common CommonFlags) error {
	ctx, span := trace.Start(ctx, "saveCache")
	defer span.End()

	span.SetAttributes(
		attribute.String("id", cache.ID),
		attribute.String("key", cache.Key),
		attribute.StringSlice("fallback_keys", cache.FallbackKeys),
		attribute.StringSlice("paths", cache.Paths),
	)

	printer.Info("❔", "Validating and preparing cache save for ID: %s", cache.ID)

	// Phase 1: Validate and prepare data
	data, err := cmd.validateAndPrepare(ctx, span, cache)
	if err != nil {

		printer.Error("❌", "Invalid cache configuration ID: %s: %s", cache.ID, err)

		return fmt.Errorf("failed to validate and prepare cache: %w", err)
	}

	printer.Info("💾", "Starting cache save for id: %s", cache.ID)
	printer.Info("🔍", "Checking if cache already exists for key: %s", data.cacheKey)

	_, exists, err := client.CachePeekExists(ctx, api.CachePeekReq{
		Key:    data.cacheKey,
		Branch: common.Branch,
	})
	if err != nil {
		return trace.NewError(span, "failed to peek cache: %w", err)
	}

	if exists {
		printer.Success("✅", "Cache already exists for key: %s", data.cacheKey)
		fmt.Println("true") // write to stdout
		return nil
	}

	printer.Info("🔍", "Looking up the cache registry for key: %s", data.cacheKey)

	cacheRegistryResp, err := client.CacheRegistry(ctx)
	if err != nil {
		return trace.NewError(span, "failed to get cache registry: %w", err)
	}

	err = validateCacheRegistry(cacheRegistryResp, common)
	if err != nil {

		printer.Error("❌", "Invalid cache store configuration: %s", err)

		return fmt.Errorf("invalid cache store configuration: %w", err)
	}

	printer.Info("📦", "Creating new cache entry for key: %s store: %s", data.cacheKey, cacheRegistryResp.Store)

	// Phase 3: Build archive
	archiveResult, err := cmd.buildArchive(ctx, span, data, printer)
	if err != nil {
		return err
	}

	// Phase 4: Register cache entry
	registrationResult, err := cmd.registerCacheEntry(ctx, span, data, archiveResult, client, common, cacheRegistryResp.Store)
	if err != nil {
		return err
	}

	printer.Info("🚀", "Registering cache entry with upload ID: %s", registrationResult.uploadID)

	// Phase 5: Upload archive
	uploadResult, err := cmd.uploadArchive(ctx, span, data.cacheKey, cacheRegistryResp.Store, archiveResult, printer, common)
	if err != nil {
		return err
	}

	// Phase 6: Commit cache
	if err := cmd.commitCache(ctx, span, registrationResult.uploadID, client); err != nil {
		return err
	}

	printer.Success("🎉", "Cache committed successfully")

	// Phase 7: Print summary
	t := table.New().
		Border(lipgloss.NormalBorder()).
		Row("Key", data.cacheKey).
		Row("Archive Size", humanize.Bytes(Int64ToUint64(archiveResult.fileInfo.Size))).
		Row("Written Bytes", humanize.Bytes(Int64ToUint64(archiveResult.fileInfo.WrittenBytes))).
		Row("Written Entries", fmt.Sprintf("%d", archiveResult.fileInfo.WrittenEntries)).
		Row("Compression Ratio", fmt.Sprintf("%.2f", compressionRatio(archiveResult.fileInfo))).
		Row("Build Duration", archiveResult.fileInfo.Duration.String()).
		Row("Transfer Speed", fmt.Sprintf("%.2fMB/s", uploadResult.transferInfo.TransferSpeed)).
		Row("Upload Duration", uploadResult.transferInfo.Duration.String()).
		Row("Paths", strings.Join(data.paths, ", "))

	printer.Info("📊", "Cache save summary:\n%s", t.Render())

	fmt.Println("true") // write to stdout

	return nil
}

type saveData struct {
	paths        []string
	cacheKey     string
	fallbackKeys []string
}

type archiveResult struct {
	fileInfo *archive.ArchiveInfo
}

type registrationResult struct {
	uploadID string
}

type uploadResult struct {
	transferInfo *store.TransferInfo
}

func validateCacheRegistry(resp api.CacheRegistryResp, common CommonFlags) error {
	if store.IsValidStore(resp.Store) {
		return fmt.Errorf("unsupported cache store: %s", resp.Store)
	}

	if resp.Store == store.LocalS3Store && !strings.HasPrefix(common.BucketURL, "s3://") {
		return fmt.Errorf("bucket URL for S3 store must start with 's3://': %s", common.BucketURL)
	}

	return nil
}

func (cmd *SaveCmd) validateAndPrepare(ctx context.Context, span oteltrace.Span, cache Cache) (*saveData, error) {
	paths, err := checkPath(cache.Paths)
	if err != nil {
		return nil, trace.NewError(span, "failed to check paths: %w", err)
	}

	cacheKey, err := key.Template(cache.ID, cache.Key, false)
	if err != nil {
		return nil, trace.NewError(span, "failed to template key: %w", err)
	}

	fallbackKeys, err := restoreKeys(cache.ID, cache.FallbackKeys)
	if err != nil {
		return nil, trace.NewError(span, "failed to restore keys: %w", err)
	}

	_, err = checkPathsExist(paths)
	if err != nil {
		return nil, trace.NewError(span, "failed to check paths exist: %w", err)
	}

	return &saveData{
		paths:        paths,
		cacheKey:     cacheKey,
		fallbackKeys: fallbackKeys,
	}, nil
}

func (cmd *SaveCmd) buildArchive(ctx context.Context, span oteltrace.Span, data *saveData, printer *console.Printer) (*archiveResult, error) {
	printer.Info("🗜️", "Building archive for paths: %v", data.paths)

	fileInfo, err := archive.BuildArchive(ctx, data.paths, data.cacheKey)
	if err != nil {
		return nil, fmt.Errorf("failed to build archive: %w", err)
	}

	printer.Success("✅", "Archive built successfully: %s (%s)",
		humanize.Bytes(Int64ToUint64(fileInfo.Size)),
		humanize.Bytes(Int64ToUint64(fileInfo.WrittenBytes)))

	log.Info().
		Str("key", data.cacheKey).
		Int64("size", fileInfo.Size).
		Str("sha256sum", fileInfo.Sha256sum).
		Dur("duration_ms", fileInfo.Duration).
		Int64("entries", fileInfo.WrittenEntries).
		Int64("bytes_written", fileInfo.WrittenBytes).
		Float64("compression_ratio", compressionRatio(fileInfo)).
		Msg("archive built")

	return &archiveResult{
		fileInfo: fileInfo,
	}, nil
}

func (cmd *SaveCmd) registerCacheEntry(ctx context.Context, span oteltrace.Span, data *saveData, archiveResult *archiveResult, client api.Client, common CommonFlags, store string) (*registrationResult, error) {
	createResp, err := client.CacheCreate(ctx, api.CacheCreateReq{
		Key:          data.cacheKey,
		FallbackKeys: data.fallbackKeys,
		Compression:  common.Format,
		FileSize:     int(archiveResult.fileInfo.Size),
		Digest:       fmt.Sprintf("sha256:%s", archiveResult.fileInfo.Sha256sum),
		Paths:        data.paths,
		Platform:     fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		Pipeline:     common.Pipeline,
		Branch:       common.Branch,
		Organization: common.Organization,
		Store:        store,
	})
	if err != nil {
		return nil, trace.NewError(span, "failed to create cache: %w", err)
	}

	return &registrationResult{
		uploadID: createResp.UploadID,
	}, nil
}

func (cmd *SaveCmd) uploadArchive(ctx context.Context, span oteltrace.Span, cacheKey string, cacheStore string, archiveResult *archiveResult, printer *console.Printer, common CommonFlags) (*uploadResult, error) {
	log.Info().
		Str("bucket_url", common.BucketURL).
		Str("prefix", common.Prefix).
		Str("store", cacheStore).
		Msg("Uploading cache archive")

	var (
		blobs store.Blob
		err   error
	)

	switch cacheStore {
	case store.LocalS3Store:
		blobs, err = store.NewGocloudBlob(ctx, common.BucketURL, common.Prefix)
		if err != nil {
			return nil, trace.NewError(span, "failed to create s3 blob store: %w", err)
		}
	case store.LocalNscStore:
		blobs = store.NewNscStore()
	default:
		return nil, trace.NewError(span, "unsupported store type: %s", cacheStore)
	}

	printer.Info("⬆️", "Uploading cache archive to S3...")

	transferInfo, err := blobs.Upload(ctx, archiveResult.fileInfo.ArchivePath, cacheKey)
	if err != nil {
		return nil, trace.NewError(span, "failed to upload cache: %w", err)
	}

	printer.Success("✅", "Upload completed: %s at %.2fMB/s",
		humanize.Bytes(Int64ToUint64(transferInfo.BytesTransferred)),
		transferInfo.TransferSpeed)

	log.Info().
		Int64("bytes_transferred", transferInfo.BytesTransferred).
		Str("transfer_speed", fmt.Sprintf("%.2fMB/s", transferInfo.TransferSpeed)).
		Str("request_id", transferInfo.RequestID).
		Dur("duration_ms", transferInfo.Duration).
		Msg("Cache uploaded")

	return &uploadResult{
		transferInfo: transferInfo,
	}, nil
}

func (cmd *SaveCmd) commitCache(ctx context.Context, span oteltrace.Span, uploadID string, client api.Client) error {
	commitResp, err := client.CacheCommit(ctx, api.CacheCommitReq{
		UploadID: uploadID,
	})
	if err != nil {
		return trace.NewError(span, "failed to commit cache: %w", err)
	}

	log.Debug().Str("message", commitResp.Message).
		Msg("Cache committed successfully")

	return nil
}

func checkPathsExist(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no paths provided")
	}

	for _, path := range paths {
		// handle ~ expansion
		if path[0] == '~' {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("failed to get home directory: %w", err)
			}
			path = homeDir + path[1:]
		}

		// current working directory
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current working directory: %w", err)
		}

		log.Info().Str("path", path).Str("cwd", cwd).Msg("checking path")

		// check if the path exists
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil, fmt.Errorf("path does not exist: %s", path)
		}
	}

	return paths, nil
}
