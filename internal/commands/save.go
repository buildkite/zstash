package commands

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/buildkite/zstash/internal/archive"
	"github.com/buildkite/zstash/internal/store"
	"github.com/buildkite/zstash/internal/trace"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
)

type SaveCmd struct {
	Key          string   `flag:"key" help:"Key of the cache entry to save, this can be a template string." required:"true"`
	RegistrySlug string   `flag:"registry-slug" help:"The registry slug to use." env:"BUILDKITE_REGISTRY_SLUG" default:"~"`
	Endpoint     string   `flag:"endpoint" help:"The endpoint to use. Defaults to the Buildkite agent API endpoint." default:"https://agent.buildkite.com/v3"`
	Store        string   `flag:"store" help:"store used to upload / download, either s3 or artifact" enum:"s3" default:"s3"`
	Format       string   `flag:"format" help:"the format of the archive" enum:"zip" default:"zip"`
	Paths        []string `arg:"" name:"path" help:"Paths to remove."`
	Organization string   `flag:"organization" help:"The organization to use." env:"BUILDKITE_ORGANIZATION_SLUG" required:"true"`
	Branch       string   `flag:"branch" help:"The branch to use." env:"BUILDKITE_BRANCH" required:"true"`
	Pipeline     string   `flag:"pipeline" help:"The pipeline to use." env:"BUILDKITE_PIPELINE_SLUG" required:"true"`
	BucketURL    string   `flag:"bucket-url" help:"The bucket URL to use." env:"BUILDKITE_CACHE_BUCKET_URL"`
	Prefix       string   `flag:"prefix" help:"The prefix to use." env:"BUILDKITE_CACHE_PREFIX"`
	Token        string   `flag:"token" help:"The buildkite agent access token to use." env:"BUILDKITE_AGENT_ACCESS_TOKEN" required:"true"`
	Skip         bool     `help:"Skip saving the cache entry." env:"BUILDKITE_CACHE_SKIP"`
}

func (cmd *SaveCmd) Run(ctx context.Context, globals *Globals) error {
	ctx, span := trace.Start(ctx, "SaveCmdRun")
	defer span.End()

	log.Info().Str("version", globals.Version).Msg("Running SaveCmd")

	span.SetAttributes(
		attribute.String("key", cmd.Key),
		attribute.StringSlice("paths", cmd.Paths),
		attribute.Bool("skip", cmd.Skip),
	)

	// check if the cache is enabled
	if cmd.Skip {
		log.Info().Msg("Skipping cache save")
		fmt.Println("false") // write to stdout
		return nil
	}

	// create a http client
	client, err := NewClient(ctx, cmd.Endpoint, cmd.RegistrySlug, cmd.Token)
	if err != nil {
		return trace.NewError(span, "failed to create client: %w", err)
	}

	// peek at the cache registry
	peek := CachePeekReq{
		Key: cmd.Key,
	}

	peekResp, exists, err := client.CachePeekExists(ctx, peek)
	if err != nil {
		return trace.NewError(span, "failed to peek cache: %w", err)
	}

	if exists {
		log.Printf("Cache already exists: %s", peekResp.Message)
		fmt.Println("true") // write to stdout
		return nil
	}

	log.Printf("Cache peeked: %v", peekResp)

	// create the cache
	_, err = checkPathsExist(cmd.Paths)
	if err != nil {
		return trace.NewError(span, "failed to check paths: %w", err)
	}

	start := time.Now()

	fileInfo, err := archive.BuildArchive(ctx, cmd.Paths, cmd.Key)
	if err != nil {
		return fmt.Errorf("failed to build archive: %w", err)
	}

	log.Info().
		Str("path", fileInfo.ArchivePath).
		Int64("size", fileInfo.Size).
		Any("stats", fileInfo.Stats).
		Str("sha256sum", fileInfo.Sha256sum).
		Dur("duration_ms", time.Since(start)).
		Msg("archive built")

	create := CacheCreateReq{
		Key:          cmd.Key,
		Compression:  cmd.Format,
		FileSize:     int(fileInfo.Size),
		Digest:       fmt.Sprintf("sha256:%s", fileInfo.Sha256sum), // "sha256:997cd98513730e9ca1beebf7f17d4625a968aabd",
		Paths:        cmd.Paths,
		Platform:     fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		Pipeline:     cmd.Pipeline,
		Branch:       cmd.Branch,
		Organization: cmd.Organization,
	}

	createResp, err := client.CacheCreate(ctx, create)
	if err != nil {
		return trace.NewError(span, "failed to create cache: %w", err)
	}

	log.Info().Str("upload_id", createResp.UploadID).Msg("Cache created")

	// upload the cache
	blobs, err := store.NewS3Blob(ctx, cmd.BucketURL, cmd.Prefix)
	if err != nil {
		return trace.NewError(span, "failed to create uploader: %w", err)
	}

	err = blobs.Upload(ctx, fileInfo.ArchivePath, cmd.Key)
	if err != nil {
		return trace.NewError(span, "failed to upload cache: %w", err)
	}

	commit := CacheCommitReq{
		UploadID: createResp.UploadID,
	}

	commitResp, err := client.CacheCommit(ctx, commit)
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

		// check if the path exists
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil, fmt.Errorf("path does not exist: %s", path)
		}
	}

	return paths, nil
}
