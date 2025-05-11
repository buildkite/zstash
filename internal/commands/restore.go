package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/buildkite/zstash/internal/archive"
	"github.com/buildkite/zstash/internal/store"
	"github.com/buildkite/zstash/internal/trace"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
)

type RestoreCmd struct {
	Key          string   `flag:"key" help:"Key of the cache entry to restore, this can be a template string." required:"true"`
	RegistrySlug string   `flag:"registry-slug" help:"The registry slug to use." env:"BUILDKITE_REGISTRY_SLUG" default:"~"`
	Endpoint     string   `flag:"endpoint" help:"The endpoint to use. Defaults to the Buildkite agent API endpoint." default:"https://agent.buildkite.com/v3"`
	Store        string   `flag:"store" help:"store used to upload / download, either s3 or artifact" enum:"s3,artifact" default:"s3"`
	Format       string   `flag:"format" help:"the format of the archive" enum:"zip" default:"zip"`
	Paths        []string `arg:"" name:"path" help:"Paths within the cache archive to restore to the restore path."`
	Organization string   `flag:"organization" help:"The organization to use." env:"BUILDKITE_ORGANIZATION_SLUG"`
	Branch       string   `flag:"branch" help:"The branch to use." env:"BUILDKITE_BRANCH"`
	Pipeline     string   `flag:"pipeline" help:"The pipeline to use." env:"BUILDKITE_PIPELINE_SLUG"`
	BucketURL    string   `flag:"bucket-url" help:"The bucket URL to use." env:"BUILDKITE_CACHE_BUCKET_URL"`
	Token        string   `flag:"token" help:"The buildkite agent access token to use." env:"BUILDKITE_AGENT_ACCESS_TOKEN" required:"true"`
	Prefix       string   `flag:"prefix" help:"The prefix to use." env:"BUILDKITE_CACHE_PREFIX"`
}

func (cmd *RestoreCmd) Run(ctx context.Context, globals *Globals) error {
	ctx, span := trace.Start(ctx, "RestoreCmdRun")
	defer span.End()

	log.Info().Str("version", globals.Version).Msg("Running RestoreCmd")

	span.SetAttributes(
		attribute.String("key", cmd.Key),
		attribute.StringSlice("paths", cmd.Paths),
	)

	// create a http client
	client, err := NewClient(ctx, cmd.Endpoint, cmd.RegistrySlug, cmd.Token)
	if err != nil {
		return trace.NewError(span, "failed to create client: %w", err)
	}

	_, exists, err := client.CacheRetrieve(ctx, CacheRetrieveReq{Key: cmd.Key})
	if err != nil {
		return trace.NewError(span, "failed to retrieve cache: %w", err)
	}

	if !exists {
		log.Warn().Str("key", cmd.Key).Msg("cache not found")
		fmt.Println(exists)

		return nil
	}

	// upload the cache
	blobs, err := store.NewS3Blob(ctx, cmd.BucketURL, cmd.Prefix)
	if err != nil {
		return trace.NewError(span, "failed to create uploader: %w", err)
	}

	// create a temp directory
	tmpDir, err := os.MkdirTemp("", "zstash-restore")
	if err != nil {
		return trace.NewError(span, "failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archiveFile := filepath.Join(tmpDir, cmd.Key)

	// download the cache
	archiveSize, err := blobs.Download(ctx, cmd.Key, archiveFile)
	if err != nil {
		return trace.NewError(span, "failed to download cache: %w", err)
	}

	// open the archive
	archiveFileHandle, err := os.Open(archiveFile)
	if err != nil {
		return trace.NewError(span, "failed to open archive file: %w", err)
	}
	defer archiveFileHandle.Close()

	log.Info().Strs("paths", cmd.Paths).Msg("extracting files")

	// extract the cache
	err = archive.ExtractFiles(ctx, archiveFileHandle, archiveSize, cmd.Paths)
	if err != nil {
		return trace.NewError(span, "failed to extract archive: %w", err)
	}

	// TODO: check if the cache entry is a fallback

	fmt.Println("true") // write to stdout

	return nil
}
