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
)

type SaveCmd struct {
	ID           string `flag:"id" help:"ID of the cache entry to save." required:"true"`
	Key          string `flag:"key" help:"Key of the cache entry to save, this can be a template string." required:"true"`
	FallbackKeys string `flag:"fallback-keys" help:"Fallback keys to use, this is a comma delimited list of key template strings."`
	Store        string `flag:"store" help:"store used to upload / download" enum:"s3" default:"s3"`
	Format       string `flag:"format" help:"the format of the archive" enum:"zip" default:"zip"`
	Paths        string `flag:"paths" help:"Paths to remove."`
	Organization string `flag:"organization" help:"The organization to use." env:"BUILDKITE_ORGANIZATION_SLUG" required:"true"`
	Branch       string `flag:"branch" help:"The branch to use." env:"BUILDKITE_BRANCH" required:"true"`
	Pipeline     string `flag:"pipeline" help:"The pipeline to use." env:"BUILDKITE_PIPELINE_SLUG" required:"true"`
	BucketURL    string `flag:"bucket-url" help:"The bucket URL to use." env:"BUILDKITE_CACHE_BUCKET_URL"`
	Prefix       string `flag:"prefix" help:"The prefix to use." env:"BUILDKITE_CACHE_PREFIX"`
	Skip         bool   `help:"Skip saving the cache entry." env:"BUILDKITE_CACHE_SKIP"`
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

	tkey, err := key.Template(cmd.ID, cmd.Key)
	if err != nil {
		return trace.NewError(span, "failed to template key: %w", err)
	}

	globals.Printer.Info("💾", "Starting cache save for id: %s", cmd.ID)
	globals.Printer.Info("🔍", "Checking if cache already exists for key: %s", tkey)

	// peek at the cache registry
	_, exists, err := globals.Client.CachePeekExists(ctx, api.CachePeekReq{
		Key:    tkey,
		Branch: cmd.Branch,
	})
	if err != nil {
		return trace.NewError(span, "failed to peek cache: %w", err)
	}

	if exists {
		globals.Printer.Success("✅", "Cache already exists for key: %s", tkey)
		fmt.Println("true") // write to stdout
		return nil
	}

	globals.Printer.Info("📦", "Creating new cache entry for key: %s", tkey)

	paths, err := checkPath(cmd.Paths)
	if err != nil {
		return trace.NewError(span, "failed to check paths: %w", err)
	}

	fallbackKeys, err := restoreKeys(cmd.ID, cmd.FallbackKeys)
	if err != nil {
		return trace.NewError(span, "failed to restore keys: %w", err)
	}

	// create the cache
	_, err = checkPathsExist(paths)
	if err != nil {
		return trace.NewError(span, "failed to check paths exist: %w", err)
	}

	globals.Printer.Info("🗜️", "Building archive for paths: %v", paths)

	fileInfo, err := archive.BuildArchive(ctx, paths, tkey)
	if err != nil {
		return fmt.Errorf("failed to build archive: %w", err)
	}

	globals.Printer.Success("✅", "Archive built successfully: %s (%s)",
		humanize.Bytes(Int64ToUint64(fileInfo.Size)),
		humanize.Bytes(Int64ToUint64(fileInfo.WrittenBytes)))

	log.Info().
		Str("key", tkey).
		Int64("size", fileInfo.Size).
		Str("sha256sum", fileInfo.Sha256sum).
		Dur("duration_ms", fileInfo.Duration).
		Int64("entries", fileInfo.WrittenEntries).
		Int64("bytes_written", fileInfo.WrittenBytes).
		Float64("compression_ratio", compressionRatio(fileInfo)).
		Msg("archive built")

	createResp, err := globals.Client.CacheCreate(ctx, api.CacheCreateReq{
		Key:          tkey,
		FallbackKeys: fallbackKeys,
		Compression:  cmd.Format,
		FileSize:     int(fileInfo.Size),
		Digest:       fmt.Sprintf("sha256:%s", fileInfo.Sha256sum), // "sha256:997cd98513730e9ca1beebf7f17d4625a968aabd",
		Paths:        paths,
		Platform:     fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		Pipeline:     cmd.Pipeline,
		Branch:       cmd.Branch,
		Organization: cmd.Organization,
	})
	if err != nil {
		return trace.NewError(span, "failed to create cache: %w", err)
	}

	globals.Printer.Info("🚀", "Registering cache entry with upload ID: %s", createResp.UploadID)

	// upload the cache
	blobs, err := store.NewGocloudBlob(ctx, cmd.BucketURL, cmd.Prefix)
	if err != nil {
		return trace.NewError(span, "failed to create uploader: %w", err)
	}

	globals.Printer.Info("⬆️", "Uploading cache archive to S3...")

	transferInfo, err := blobs.Upload(ctx, fileInfo.ArchivePath, tkey)
	if err != nil {
		return trace.NewError(span, "failed to upload cache: %w", err)
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

	commitResp, err := globals.Client.CacheCommit(ctx, api.CacheCommitReq{
		UploadID: createResp.UploadID,
	})
	if err != nil {
		return trace.NewError(span, "failed to commit cache: %w", err)
	}

	log.Debug().Str("message", commitResp.Message).
		Msg("Cache committed successfully")

	globals.Printer.Success("🎉", "Cache committed successfully")

	// Create summary table
	t := table.New().
		Border(lipgloss.NormalBorder()).
		Row("Key", tkey).
		Row("Archive Size", humanize.Bytes(Int64ToUint64(fileInfo.Size))).
		Row("Written Bytes", humanize.Bytes(Int64ToUint64(fileInfo.WrittenBytes))).
		Row("Written Entries", fmt.Sprintf("%d", fileInfo.WrittenEntries)).
		Row("Compression Ratio", fmt.Sprintf("%.2f", compressionRatio(fileInfo))).
		Row("Build Duration", fileInfo.Duration.String()).
		Row("Transfer Speed", fmt.Sprintf("%.2fMB/s", transferInfo.TransferSpeed)).
		Row("Upload Duration", transferInfo.Duration.String()).
		Row("Paths", strings.Join(paths, ", "))

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
