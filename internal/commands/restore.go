package commands

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

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
)

type RestoreCmd struct {
	ID           string `flag:"id" help:"ID of the cache entry to restore." required:"true"`
	Key          string `flag:"key" help:"Key of the cache entry to restore, this can be a template string." required:"true"`
	FallbackKeys string `flag:"fallback-keys" help:"Fallback keys to use, this is a comma delimited list of key template strings."`
	Store        string `flag:"store" help:"store used to upload / download" enum:"s3" default:"s3"`
	Format       string `flag:"format" help:"the format of the archive" enum:"zip" default:"zip"`
	Paths        string `flag:"paths" help:"Paths within the cache archive to restore to the restore path."`
	Organization string `flag:"organization" help:"The organization to use." env:"BUILDKITE_ORGANIZATION_SLUG"`
	Branch       string `flag:"branch" help:"The branch to use." env:"BUILDKITE_BRANCH"`
	Pipeline     string `flag:"pipeline" help:"The pipeline to use." env:"BUILDKITE_PIPELINE_SLUG"`
	BucketURL    string `flag:"bucket-url" help:"The bucket URL to use." env:"BUILDKITE_CACHE_BUCKET_URL"`
	Prefix       string `flag:"prefix" help:"The prefix to use." env:"BUILDKITE_CACHE_PREFIX"`
	S3Endpoint   string `flag:"s3-endpoint" help:"The S3 endpoint to use." env:"BUILDKITE_CACHE_S3_ENDPOINT"`
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

	paths, err := checkPath(cmd.Paths)
	if err != nil {
		return trace.NewError(span, "failed to check paths: %w", err)
	}

	cacheKey, err := key.Template(cmd.ID, cmd.Key)
	if err != nil {
		return trace.NewError(span, "failed to template key: %w", err)
	}

	fallbackCacheKeys, err := restoreKeys(cmd.ID, cmd.FallbackKeys)
	if err != nil {
		return trace.NewError(span, "failed to restore keys: %w", err)
	}

	globals.Printer.Info("♻️", "Starting restore for id: %s", cmd.ID)
	globals.Printer.Info("🔍", "Search registry: default") // TODO: configurable registries

	log.Info().
		Str("key", cacheKey).
		Strs("fallback_keys", fallbackCacheKeys).
		Msg("calling cache retrieve")

	_, exists, err := globals.Client.CacheRetrieve(ctx, api.CacheRetrieveReq{Key: cacheKey, Branch: cmd.Branch, FallbackKeys: strings.Join(fallbackCacheKeys, ",")})
	if err != nil {
		return trace.NewError(span, "failed to retrieve cache: %w", err)
	}

	if !exists {
		globals.Printer.Warn("💨", "Cache miss for key: %s", cacheKey)

		fmt.Println(exists)

		return nil
	}

	globals.Printer.Success("🎯", "Cache hit for key: %s", cacheKey)

	log.Info().Str("bucket_url", cmd.BucketURL).
		Str("prefix", cmd.Prefix).
		Msg("restoring cache from s3")

	globals.Printer.Info("⬇️", "Downloading cache for key: %s", cacheKey)

	// upload the cache
	blobs, err := store.NewS3Blob(ctx, cmd.BucketURL, cmd.Prefix, cmd.S3Endpoint)
	if err != nil {
		return trace.NewError(span, "failed to create uploader: %w", err)
	}

	// create a temp directory
	tmpDir, err := os.MkdirTemp("", "zstash-restore")
	if err != nil {
		return trace.NewError(span, "failed to create temp directory: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	archiveFile := filepath.Join(tmpDir, cacheKey)

	// download the cache
	transferInfo, err := blobs.Download(ctx, cacheKey, archiveFile)
	if err != nil {
		return trace.NewError(span, "failed to download cache: %w", err)
	}

	globals.Printer.Info("✅", "Downloaded cache for key: %s", humanize.Bytes(Int64ToUint64(transferInfo.BytesTransferred)))

	log.Debug().
		Int64("size", transferInfo.BytesTransferred).
		Str("transfer_speed", fmt.Sprintf("%.2fMB/s", transferInfo.TransferSpeed)).
		Str("request_id", transferInfo.RequestID).
		Dur("duration_ms", transferInfo.Duration).
		Msg("cache downloaded")

	// open the archive
	archiveFileHandle, err := os.Open(archiveFile)
	if err != nil {
		return trace.NewError(span, "failed to open archive file: %w", err)
	}
	defer func() {
		_ = archiveFileHandle.Close()
	}()

	log.Info().Strs("paths", paths).Msg("extracting files")

	// extract the cache
	archiveInfo, err := archive.ExtractFiles(ctx, archiveFileHandle, transferInfo.BytesTransferred, paths)
	if err != nil {
		return trace.NewError(span, "failed to extract archive: %w", err)
	}

	globals.Printer.Success("🗜️", "Extracted %d files from cache archive for paths: %v", archiveInfo.WrittenEntries, paths)

	log.Debug().
		Int64("size", archiveInfo.Size).
		Dur("duration_ms", archiveInfo.Duration).
		Int64("written_bytes", archiveInfo.WrittenBytes).
		Int64("written_entries", archiveInfo.WrittenEntries).
		Float64("compression_ratio", compressionRatio(archiveInfo)).
		Msg("archive extracted")

	// Title("Cache Restore Summary").
	// TitleStyle(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")))
	t := table.New().
		Border(lipgloss.NormalBorder()).
		Row("Key", cacheKey).
		Row("Size", humanize.Bytes(Int64ToUint64(archiveInfo.Size))).
		Row("Written Bytes", humanize.Bytes(Int64ToUint64(archiveInfo.WrittenBytes))).
		Row("Written Entries", fmt.Sprintf("%d", archiveInfo.WrittenEntries)).
		Row("Compression Ratio", fmt.Sprintf("%.2f", compressionRatio(archiveInfo))).
		Row("Duration", archiveInfo.Duration.String()).
		Row("Paths", strings.Join(paths, ", "))

	globals.Printer.Info("📊", "Cache restore summary:\n%s", t.Render())

	// TODO: check if the cache entry is a fallback

	fmt.Println("true") // write to stdout

	return nil
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
