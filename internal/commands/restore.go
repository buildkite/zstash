package commands

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/buildkite/zstash/internal/api"
	"github.com/buildkite/zstash/internal/archive"
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

const (
	CacheRestoreHit  = "true"
	CacheRestoreMiss = "false"
)

type RestoreCmd struct {
	ID                 string `flag:"id" help:"ID of the cache entry to restore." required:"true"`
	Key                string `flag:"key" help:"Key of the cache entry to restore, this can be a template string." required:"true"`
	FallbackKeys       string `flag:"fallback-keys" help:"Fallback keys to use, this is a comma delimited list of key template strings."`
	RecursiveChecksums bool   `flag:"recursive-checksums" help:"Recursively search for matches when generating cache keys."`
	Store              string `flag:"store" help:"store used to upload / download" enum:"s3" default:"s3"`
	Format             string `flag:"format" help:"the format of the archive" enum:"zip" default:"zip"`
	Paths              string `flag:"paths" help:"Paths within the cache archive to restore to the restore path."`
	Organization       string `flag:"organization" help:"The organization to use." env:"BUILDKITE_ORGANIZATION_SLUG"`
	Branch             string `flag:"branch" help:"The branch to use." env:"BUILDKITE_BRANCH"`
	Pipeline           string `flag:"pipeline" help:"The pipeline to use." env:"BUILDKITE_PIPELINE_SLUG"`
	BucketURL          string `flag:"bucket-url" help:"The bucket URL to use." env:"BUILDKITE_CACHE_BUCKET_URL"`
	Prefix             string `flag:"prefix" help:"The prefix to use." env:"BUILDKITE_CACHE_PREFIX"`
}

func (cmd *RestoreCmd) Run(ctx context.Context, globals *Globals) error {
	ctx, span := trace.Start(ctx, "RestoreCmdRun")
	defer span.End()

	log.Info().Str("version", globals.Version).Msg("Running RestoreCmd")

	span.SetAttributes(
		attribute.String("key", cmd.Key),
		attribute.String("paths", cmd.Paths),
		attribute.String("fallback_keys", cmd.FallbackKeys),
	)

	// Phase 1: Validate and prepare data
	data, err := cmd.validateAndPrepare(ctx, span)
	if err != nil {
		return err
	}

	globals.Printer.Info("♻️", "Starting restore for id: %s", cmd.ID)
	globals.Printer.Info("🔍", "Search registry: default") // TODO: configurable registries

	// Phase 2: Check if cache exists
	cacheResult, err := cmd.checkCacheExists(ctx, span, data, globals)
	if err != nil {
		return err
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
	downloadResult, err := cmd.downloadCache(ctx, span, cacheResult.cacheKey, globals)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.RemoveAll(downloadResult.tmpDir)
	}()

	globals.Printer.Success("✅", "Download completed: %s at %.2fMB/s",
		humanize.Bytes(Int64ToUint64(downloadResult.transferInfo.BytesTransferred)),
		downloadResult.transferInfo.TransferSpeed)

	// Phase 4: Extract files
	extractionResult, err := cmd.extractFiles(ctx, span, downloadResult, data.paths, globals)
	if err != nil {
		return err
	}

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
	exists    bool
	cacheKey  string
	fallback  bool
	expiresAt time.Time
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

func (cmd *RestoreCmd) validateAndPrepare(ctx context.Context, span oteltrace.Span) (*restoreData, error) {
	paths, err := checkPath(cmd.Paths)
	if err != nil {
		return nil, trace.NewError(span, "failed to check paths: %w", err)
	}

	cacheKey, err := key.Template(cmd.ID, cmd.Key, cmd.RecursiveChecksums)
	if err != nil {
		return nil, trace.NewError(span, "failed to template key: %w", err)
	}

	fallbackCacheKeys, err := restoreKeys(cmd.ID, cmd.FallbackKeys, cmd.RecursiveChecksums)
	if err != nil {
		return nil, trace.NewError(span, "failed to restore keys: %w", err)
	}

	return &restoreData{
		paths:             paths,
		cacheKey:          cacheKey,
		fallbackCacheKeys: fallbackCacheKeys,
	}, nil
}

func (cmd *RestoreCmd) checkCacheExists(ctx context.Context, span oteltrace.Span, data *restoreData, globals *Globals) (*cacheExistenceResult, error) {

	log.Info().
		Str("key", data.cacheKey).
		Strs("fallback_keys", data.fallbackCacheKeys).
		Msg("calling cache retrieve")

	retrieveResp, exists, err := globals.Client.CacheRetrieve(ctx, api.CacheRetrieveReq{
		Key:          data.cacheKey,
		Branch:       cmd.Branch,
		FallbackKeys: strings.Join(data.fallbackCacheKeys, ","),
	})
	if err != nil {
		return nil, trace.NewError(span, "failed to retrieve cache: %w", err)
	}

	if !exists {
		return &cacheExistenceResult{exists: false, cacheKey: data.cacheKey}, nil
	}

	return &cacheExistenceResult{exists: exists, cacheKey: retrieveResp.Key, fallback: retrieveResp.Fallback, expiresAt: retrieveResp.ExpiresAt}, nil
}

func (cmd *RestoreCmd) downloadCache(ctx context.Context, span oteltrace.Span, cacheKey string, globals *Globals) (*downloadResult, error) {
	log.Info().Str("bucket_url", cmd.BucketURL).
		Str("prefix", cmd.Prefix).
		Msg("restoring cache from s3")

	blobs, err := store.NewGocloudBlob(ctx, cmd.BucketURL, cmd.Prefix)
	if err != nil {
		return nil, trace.NewError(span, "failed to create uploader: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "zstash-restore")
	if err != nil {
		return nil, trace.NewError(span, "failed to create temp directory: %w", err)
	}

	archiveFile := filepath.Join(tmpDir, cacheKey)

	transferInfo, err := blobs.Download(ctx, cacheKey, archiveFile)
	if err != nil {
		// Clean up temporary directory if download fails
		if err := os.RemoveAll(tmpDir); err != nil {
			log.Error().Err(err).Str("tmpDir", tmpDir).Msg("failed to clean up temporary directory")
		}
		return nil, trace.NewError(span, "failed to download cache: %w", err)
	}

	log.Debug().
		Int64("size", transferInfo.BytesTransferred).
		Str("transfer_speed", fmt.Sprintf("%.2fMB/s", transferInfo.TransferSpeed)).
		Str("request_id", transferInfo.RequestID).
		Dur("duration_ms", transferInfo.Duration).
		Msg("cache downloaded")

	return &downloadResult{
		archiveFile:  archiveFile,
		transferInfo: transferInfo,
		tmpDir:       tmpDir,
	}, nil
}

func (cmd *RestoreCmd) extractFiles(ctx context.Context, span oteltrace.Span, download *downloadResult, paths []string, globals *Globals) (*extractionResult, error) {
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

	globals.Printer.Success("🗜️", "Extracted %d files from cache archive for paths: %v", archiveInfo.WrittenEntries, paths)

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

// calculate the compression ratio
func compressionRatio(archiveInfo *archive.ArchiveInfo) float64 {
	if archiveInfo.Size == 0 {
		return 0.0
	}
	return float64(archiveInfo.WrittenBytes) / float64(archiveInfo.Size)
}

func Int64ToUint64(x int64) uint64 {
	if x < 0 {
		return 0
	}
	if x == math.MaxInt64 {
		return math.MaxUint64
	}
	return uint64(x)
}
