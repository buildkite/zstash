package commands

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/buildkite/zstash/internal/api"
	"github.com/buildkite/zstash/internal/archive"
	"github.com/buildkite/zstash/internal/key"
	"github.com/buildkite/zstash/internal/store"
	"github.com/buildkite/zstash/internal/trace"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
)

type SaveCmd struct {
	ID           string `flag:"id" help:"ID of the cache entry to save." required:"true"`
	Key          string `flag:"key" help:"Key of the cache entry to save, this can be a template string." required:"true"`
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
		attribute.String("key", cmd.Key),
		attribute.String("paths", cmd.Paths),
		attribute.Bool("skip", cmd.Skip),
	)

	// check if the cache is enabled
	if cmd.Skip {
		log.Info().Msg("Skipping cache save")
		fmt.Println("false") // write to stdout
		return nil
	}

	tkey, err := key.Template(cmd.ID, cmd.Key)
	if err != nil {
		return trace.NewError(span, "failed to template key: %w", err)
	}

	// peek at the cache registry
	peekResp, exists, err := globals.Client.CachePeekExists(ctx, api.CachePeekReq{
		Key: tkey,
	})
	if err != nil {
		return trace.NewError(span, "failed to peek cache: %w", err)
	}

	if exists {
		log.Printf("Cache already exists: %s", peekResp.Message)
		fmt.Println("true") // write to stdout
		return nil
	}

	log.Printf("Cache peeked: %v", peekResp)

	paths, err := checkPath(cmd.Paths)
	if err != nil {
		return trace.NewError(span, "failed to check paths: %w", err)
	}

	// create the cache
	_, err = checkPathsExist(paths)
	if err != nil {
		return trace.NewError(span, "failed to check paths exist: %w", err)
	}

	fileInfo, err := archive.BuildArchive(ctx, paths, tkey)
	if err != nil {
		return fmt.Errorf("failed to build archive: %w", err)
	}

	log.Info().
		Int64("size", fileInfo.Size).
		Str("sha256sum", fileInfo.Sha256sum).
		Dur("duration_ms", fileInfo.Duration).
		Int64("entries", fileInfo.WrittenEntries).
		Int64("bytes_written", fileInfo.WrittenBytes).
		Float64("compression_ratio", compressionRatio(fileInfo)).
		Msg("archive built")

	createResp, err := globals.Client.CacheCreate(ctx, api.CacheCreateReq{
		Key:          tkey,
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

	log.Info().Str("upload_id", createResp.UploadID).Msg("Cache created")

	// upload the cache
	blobs, err := store.NewS3Blob(ctx, cmd.BucketURL, cmd.Prefix)
	if err != nil {
		return trace.NewError(span, "failed to create uploader: %w", err)
	}

	transferInfo, err := blobs.Upload(ctx, fileInfo.ArchivePath, tkey)
	if err != nil {
		return trace.NewError(span, "failed to upload cache: %w", err)
	}

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

	log.Printf("Cache committed: %v", commitResp)

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
