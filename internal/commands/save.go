package commands

import (
	"context"
	"fmt"
	"os"
	"runtime"
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
	oteltrace "go.opentelemetry.io/otel/trace"
)

type SaveCmd struct {
	ID                 string `flag:"id" help:"ID of the cache entry to save." required:"true"`
	Key                string `flag:"key" help:"Key of the cache entry to save, this can be a template string." required:"true"`
	FallbackKeys       string `flag:"fallback-keys" help:"Fallback keys to use, this is a comma delimited list of key template strings."`
	RecursiveChecksums bool   `flag:"recursive-checksums" help:"Recursively search for matches when generating cache keys."`
	Store              string `flag:"store" help:"store used to upload / download" enum:"s3,nsc" default:"s3"`
	Format             string `flag:"format" help:"the format of the archive" enum:"zip" default:"zip"`
	Paths              string `flag:"paths" help:"Paths to remove."`
	Organization       string `flag:"organization" help:"The organization to use." env:"BUILDKITE_ORGANIZATION_SLUG" required:"true"`
	Branch             string `flag:"branch" help:"The branch to use." env:"BUILDKITE_BRANCH" required:"true"`
	Pipeline           string `flag:"pipeline" help:"The pipeline to use." env:"BUILDKITE_PIPELINE_SLUG" required:"true"`
	BucketURL          string `flag:"bucket-url" help:"The bucket URL to use." env:"BUILDKITE_CACHE_BUCKET_URL"`
	Prefix             string `flag:"prefix" help:"The prefix to use." env:"BUILDKITE_CACHE_PREFIX"`
	Skip               bool   `help:"Skip saving the cache entry." env:"BUILDKITE_CACHE_SKIP"`
}

func (cmd *SaveCmd) Run(ctx context.Context, globals *Globals) error {
	ctx, span := trace.Start(ctx, "SaveCmdRun")
	defer span.End()

	log.Info().Str("version", globals.Version).Msg("Running SaveCmd")

	span.SetAttributes(
		attribute.String("id", cmd.ID),
		attribute.String("key", cmd.Key),
		attribute.String("fallback_keys", cmd.FallbackKeys),
		attribute.String("paths", cmd.Paths),
		attribute.Bool("skip", cmd.Skip),
	)

	// check if the cache is enabled
	if cmd.Skip {
		globals.Printer.Info("⏭️", "Skipping cache save for id: %s", cmd.ID)
		fmt.Println("false") // write to stdout
		return nil
	}

	// Phase 1: Validate and prepare data
	data, err := cmd.validateAndPrepare(ctx, span)
	if err != nil {
		return err
	}

	globals.Printer.Info("💾", "Starting cache save for id: %s", cmd.ID)
	globals.Printer.Info("🔍", "Checking if cache already exists for key: %s", data.cacheKey)

	// Phase 2: Check if cache already exists
	exists, err := cmd.checkCacheExists(ctx, span, data.cacheKey, globals)
	if err != nil {
		return err
	}

	if exists {
		globals.Printer.Success("✅", "Cache already exists for key: %s", data.cacheKey)
		fmt.Println("true") // write to stdout
		return nil
	}

	globals.Printer.Info("📦", "Creating new cache entry for key: %s", data.cacheKey)

	// Phase 3: Build archive
	archiveResult, err := cmd.buildArchive(ctx, span, data, globals)
	if err != nil {
		return err
	}

	// Phase 4: Register cache entry
	registrationResult, err := cmd.registerCacheEntry(ctx, span, data, archiveResult, globals)
	if err != nil {
		return err
	}

	// Phase 5: Upload archive
	uploadResult, err := cmd.uploadArchive(ctx, span, data.cacheKey, archiveResult, globals)
	if err != nil {
		return err
	}

	// Phase 6: Commit cache
	if err := cmd.commitCache(ctx, span, registrationResult.uploadID, globals); err != nil {
		return err
	}

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

	globals.Printer.Info("📊", "Cache save summary:\n%s", t.Render())

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

func (cmd *SaveCmd) validateAndPrepare(ctx context.Context, span oteltrace.Span) (*saveData, error) {
	paths, err := checkPath(cmd.Paths)
	if err != nil {
		return nil, trace.NewError(span, "failed to check paths: %w", err)
	}

	cacheKey, err := key.Template(cmd.ID, cmd.Key, cmd.RecursiveChecksums)
	if err != nil {
		return nil, trace.NewError(span, "failed to template key: %w", err)
	}

	fallbackKeys, err := restoreKeys(cmd.ID, cmd.FallbackKeys, cmd.RecursiveChecksums)
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

func (cmd *SaveCmd) checkCacheExists(ctx context.Context, span oteltrace.Span, cacheKey string, globals *Globals) (bool, error) {
	_, exists, err := globals.Client.CachePeekExists(ctx, api.CachePeekReq{
		Key:    cacheKey,
		Branch: cmd.Branch,
	})
	if err != nil {
		return false, trace.NewError(span, "failed to peek cache: %w", err)
	}

	return exists, nil
}

func (cmd *SaveCmd) buildArchive(ctx context.Context, span oteltrace.Span, data *saveData, globals *Globals) (*archiveResult, error) {
	globals.Printer.Info("🗜️", "Building archive for paths: %v", data.paths)

	fileInfo, err := archive.BuildArchive(ctx, data.paths, data.cacheKey)
	if err != nil {
		return nil, fmt.Errorf("failed to build archive: %w", err)
	}

	globals.Printer.Success("✅", "Archive built successfully: %s (%s)",
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

func (cmd *SaveCmd) registerCacheEntry(ctx context.Context, span oteltrace.Span, data *saveData, archiveResult *archiveResult, globals *Globals) (*registrationResult, error) {
	createResp, err := globals.Client.CacheCreate(ctx, api.CacheCreateReq{
		Key:          data.cacheKey,
		FallbackKeys: data.fallbackKeys,
		Compression:  cmd.Format,
		FileSize:     int(archiveResult.fileInfo.Size),
		Digest:       fmt.Sprintf("sha256:%s", archiveResult.fileInfo.Sha256sum),
		Paths:        data.paths,
		Platform:     fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		Pipeline:     cmd.Pipeline,
		Branch:       cmd.Branch,
		Organization: cmd.Organization,
	})
	if err != nil {
		return nil, trace.NewError(span, "failed to create cache: %w", err)
	}

	globals.Printer.Info("🚀", "Registering cache entry with upload ID: %s", createResp.UploadID)

	return &registrationResult{
		uploadID: createResp.UploadID,
	}, nil
}

func (cmd *SaveCmd) uploadArchive(ctx context.Context, span oteltrace.Span, cacheKey string, archiveResult *archiveResult, globals *Globals) (*uploadResult, error) {
	var (
		blobs store.Blob
		err   error
	)

	switch cmd.Store {
	case "s3":
		blobs, err = store.NewGocloudBlob(ctx, cmd.BucketURL, cmd.Prefix)
		if err != nil {
			return nil, trace.NewError(span, "failed to create s3 blob store: %w", err)
		}
	case "nsc":
		blobs = store.NewNscStore()
	default:
		return nil, trace.NewError(span, "unsupported store type: %s", cmd.Store)
	}

	globals.Printer.Info("⬆️", "Uploading cache archive to S3...")

	transferInfo, err := blobs.Upload(ctx, archiveResult.fileInfo.ArchivePath, cacheKey)
	if err != nil {
		return nil, trace.NewError(span, "failed to upload cache: %w", err)
	}

	globals.Printer.Success("✅", "Upload completed: %s at %.2fMB/s",
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

func (cmd *SaveCmd) commitCache(ctx context.Context, span oteltrace.Span, uploadID string, globals *Globals) error {
	commitResp, err := globals.Client.CacheCommit(ctx, api.CacheCommitReq{
		UploadID: uploadID,
	})
	if err != nil {
		return trace.NewError(span, "failed to commit cache: %w", err)
	}

	log.Debug().Str("message", commitResp.Message).
		Msg("Cache committed successfully")

	globals.Printer.Success("🎉", "Cache committed successfully")

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
