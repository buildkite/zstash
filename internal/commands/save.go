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
	"github.com/buildkite/zstash/internal/cache"
	"github.com/buildkite/zstash/internal/configuration"
	"github.com/buildkite/zstash/internal/store"
	"github.com/buildkite/zstash/internal/trace"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/dustin/go-humanize"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
)

type SaveCmd struct {
	Ids []string `flag:"ids" help:"List of comma delimited cache IDs to save, defaults to all." env:"BUILDKITE_CACHE_IDS"`
}

func (cmd *SaveCmd) Run(ctx context.Context, globals *Globals) error {
	ctx, span := trace.Start(ctx, "SaveCmdRun")
	defer span.End()

	log.Info().Str("version", globals.Version).Msg("Running SaveCmd")

	// Augment `cli.Caches` with template values.
	caches, err := configuration.ExpandCacheConfiguration(globals.Caches)
	if err != nil {
		return fmt.Errorf("failed to load cache configuration: %w", err)
	}

	for _, cache := range caches {
		if len(cmd.Ids) > 0 && !slices.Contains(cmd.Ids, cache.ID) {
			log.Debug().Str("id", cache.ID).Msg("Skipping cache save for ID")
			continue
		}

		if err := cache.Validate(); err != nil {
			return fmt.Errorf("cache validation failed for ID %s: %w", cache.ID, err)
		}

		if err := cmd.saveCache(ctx, cache, globals); err != nil {
			return err
		}
	}

	return nil
}

func (cmd *SaveCmd) saveCache(ctx context.Context, cache cache.Cache, globals *Globals) error {
	ctx, span := trace.Start(ctx, "saveCache")
	defer span.End()

	span.SetAttributes(
		attribute.String("id", cache.ID),
		attribute.String("key", cache.Key),
		attribute.String("registry", cache.Registry),
		attribute.StringSlice("fallback_keys", cache.FallbackKeys),
		attribute.StringSlice("paths", cache.Paths),
	)

	if cache.Registry == "" {
		cache.Registry = "~" // default to "~" if not set
	}

	globals.Printer.Info("❔", "Validating and preparing cache save for Registry: %s ID: %s ", cache.Registry, cache.ID)

	_, err := checkPathsExist(cache.Paths)
	if err != nil {

		globals.Printer.Error("❌", "Invalid cache configuration ID: %s: %s", cache.ID, err)

		return trace.NewError(span, "failed to validate and prepare cache: %w", err)
	}

	globals.Printer.Info("💾", "Starting cache save for id: %s", cache.ID)
	globals.Printer.Info("🔍", "Checking if cache already exists for key: %s", cache.Key)

	_, exists, err := globals.Client.CachePeekExists(ctx, cache.Registry, api.CachePeekReq{
		Key:    cache.Key,
		Branch: globals.Common.Branch,
	})
	if err != nil {
		return trace.NewError(span, "failed to peek cache: %w", err)
	}

	if exists {
		globals.Printer.Success("✅", "Cache already exists for key: %s", cache.Key)
		fmt.Println("true") // write to stdout
		return nil
	}

	globals.Printer.Info("🔍", "Looking up the cache registry for key: %s", cache.Key)

	cacheRegistryResp, err := globals.Client.CacheRegistry(ctx, cache.Registry)
	if err != nil {
		return trace.NewError(span, "failed to get cache registry: %w", err)
	}

	err = validateCacheRegistry(cacheRegistryResp.Store, globals.Common)
	if err != nil {

		globals.Printer.Error("❌", "Invalid cache store configuration: %s", err)

		return fmt.Errorf("invalid cache store configuration: %w", err)
	}

	globals.Printer.Info("📦", "Creating new cache entry for key: %s store: %s", cache.Key, cacheRegistryResp.Store)

	// Phase 2: Build archive
	globals.Printer.Info("🗜️", "Building archive for paths: %v", cache.Paths)

	fileInfo, err := archive.BuildArchive(ctx, cache.Paths, cache.Key)
	if err != nil {
		return fmt.Errorf("failed to build archive: %w", err)
	}

	globals.Printer.Success("✅", "Archive built successfully: %s (%s)",
		humanize.Bytes(Int64ToUint64(fileInfo.Size)),
		humanize.Bytes(Int64ToUint64(fileInfo.WrittenBytes)))

	log.Info().
		Str("key", cache.Key).
		Int64("size", fileInfo.Size).
		Str("sha256sum", fileInfo.Sha256sum).
		Dur("duration_ms", fileInfo.Duration).
		Int64("entries", fileInfo.WrittenEntries).
		Int64("bytes_written", fileInfo.WrittenBytes).
		Float64("compression_ratio", compressionRatio(fileInfo)).
		Msg("archive built")

	createResp, err := globals.Client.CacheCreate(ctx, cacheRegistryResp.Name, api.CacheCreateReq{
		Key:          cache.Key,
		FallbackKeys: cache.FallbackKeys,
		Compression:  globals.Common.Format,
		FileSize:     int(fileInfo.Size),
		Digest:       fmt.Sprintf("sha256:%s", fileInfo.Sha256sum),
		Paths:        cache.Paths,
		Platform:     fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		Pipeline:     globals.Common.Pipeline,
		Branch:       globals.Common.Branch,
		Organization: globals.Common.Organization,
		Store:        cacheRegistryResp.Store,
	})
	if err != nil {
		return fmt.Errorf("failed to create cache entry: %w", err)
	}

	globals.Printer.Info("🚀", "Registering cache entry with upload ID: %s", createResp.UploadID)

	// Phase 4: Upload archive
	// uploadResult, err := cmd.uploadArchive(ctx, registrationResult, cacheRegistryResp.Store, archiveResult, globals.Printer, globals.Common)

	log.Info().
		Str("bucket_url", globals.Common.BucketURL).
		Str("store_object_name", createResp.StoreObjectName).
		Str("store", cacheRegistryResp.Store).
		Msg("Uploading cache archive")

	var (
		blobs store.Blob
	)

	blobs, err = store.NewBlobStore(ctx, cacheRegistryResp.Store, globals.Common.BucketURL)
	if err != nil {
		return fmt.Errorf("failed to create blob store: %w", err)
	}

	globals.Printer.Info("⬆️", "Uploading cache archive...")

	transferInfo, err := blobs.Upload(ctx, fileInfo.ArchivePath, createResp.StoreObjectName)
	if err != nil {
		return fmt.Errorf("failed to upload cache: %w", err)
	}

	globals.Printer.Success("✅", "Upload completed: %s at %.2fMB/s",
		humanize.Bytes(Int64ToUint64(transferInfo.BytesTransferred)),
		transferInfo.TransferSpeed)

	log.Info().
		Int64("bytes_transferred", transferInfo.BytesTransferred).
		Str("transfer_speed", fmt.Sprintf("%.2fMB/s", transferInfo.TransferSpeed)).
		Str("request_id", transferInfo.RequestID).
		Dur("duration_ms", transferInfo.Duration).
		Str("store_object_name", createResp.StoreObjectName).
		Msg("Cache uploaded")

	commitResp, err := globals.Client.CacheCommit(ctx, cache.Registry, api.CacheCommitReq{
		UploadID: createResp.UploadID,
	})
	if err != nil {
		return fmt.Errorf("failed to commit cache: %w", err)
	}

	log.Debug().Str("message", commitResp.Message).
		Msg("Cache committed successfully")

	globals.Printer.Success("🎉", "Cache committed successfully")

	// Phase 6: Print summary
	t := table.New().
		Border(lipgloss.NormalBorder()).
		Row("Key", cache.Key).
		Row("Archive Size", humanize.Bytes(Int64ToUint64(fileInfo.Size))).
		Row("Written Bytes", humanize.Bytes(Int64ToUint64(fileInfo.WrittenBytes))).
		Row("Written Entries", fmt.Sprintf("%d", fileInfo.WrittenEntries)).
		Row("Compression Ratio", fmt.Sprintf("%.2f", compressionRatio(fileInfo))).
		Row("Build Duration", fileInfo.Duration.String()).
		Row("Transfer Speed", fmt.Sprintf("%.2fMB/s", transferInfo.TransferSpeed)).
		Row("Upload Duration", transferInfo.Duration.String()).
		Row("Paths", strings.Join(cache.Paths, ", "))

	globals.Printer.Info("📊", "Cache save summary:\n%s", t.Render())

	fmt.Println("true") // write to stdout

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

func validateCacheRegistry(storeVal string, common CommonFlags) error {
	if !store.IsValidStore(storeVal) {
		return fmt.Errorf("unsupported cache store: %s", storeVal)
	}

	switch storeVal {
	case store.LocalS3Store:
		if !strings.HasPrefix(common.BucketURL, "s3://") && !strings.HasPrefix(common.BucketURL, "file://") {
			return fmt.Errorf("bucket URL for S3 store must start with 's3://' or 'file://': %s", common.BucketURL)
		}
	case store.LocalHostedAgents:
		if common.BucketURL != "" {
			return fmt.Errorf("NSC store should not have bucket URL set, got: %s", common.BucketURL)
		}
	}

	return nil
}
