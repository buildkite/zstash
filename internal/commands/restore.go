package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/buildkite/zstash/internal/api"
	"github.com/buildkite/zstash/internal/archive"
	"github.com/buildkite/zstash/internal/key"
	"github.com/buildkite/zstash/internal/store"
	"github.com/buildkite/zstash/internal/trace"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
)

type RestoreCmd struct {
	ID           string `flag:"id" help:"ID of the cache entry to restore." required:"true"`
	Key          string `flag:"key" help:"Key of the cache entry to restore, this can be a template string." required:"true"`
	Store        string `flag:"store" help:"store used to upload / download" enum:"s3" default:"s3"`
	Format       string `flag:"format" help:"the format of the archive" enum:"zip" default:"zip"`
	Paths        string `flag:"paths" help:"Paths within the cache archive to restore to the restore path."`
	Organization string `flag:"organization" help:"The organization to use." env:"BUILDKITE_ORGANIZATION_SLUG"`
	Branch       string `flag:"branch" help:"The branch to use." env:"BUILDKITE_BRANCH"`
	Pipeline     string `flag:"pipeline" help:"The pipeline to use." env:"BUILDKITE_PIPELINE_SLUG"`
	BucketURL    string `flag:"bucket-url" help:"The bucket URL to use." env:"BUILDKITE_CACHE_BUCKET_URL"`
	Prefix       string `flag:"prefix" help:"The prefix to use." env:"BUILDKITE_CACHE_PREFIX"`
}

func (cmd *RestoreCmd) Run(ctx context.Context, globals *Globals) error {
	ctx, span := trace.Start(ctx, "RestoreCmdRun")
	defer span.End()

	log.Info().Str("version", globals.Version).Msg("Running RestoreCmd")

	span.SetAttributes(
		attribute.String("key", cmd.Key),
		attribute.String("paths", cmd.Paths),
	)

	tkey, err := key.Template(cmd.ID, cmd.Key)
	if err != nil {
		return trace.NewError(span, "failed to template key: %w", err)
	}

	paths, err := checkPath(cmd.Paths)
	if err != nil {
		return trace.NewError(span, "failed to check paths: %w", err)
	}

	_, exists, err := globals.Client.CacheRetrieve(ctx, api.CacheRetrieveReq{Key: tkey})
	if err != nil {
		return trace.NewError(span, "failed to retrieve cache: %w", err)
	}

	if !exists {
		log.Warn().Str("key", tkey).Msg("cache not found")
		fmt.Println(exists)

		return nil
	}

	log.Info().Str("bucket_url", cmd.BucketURL).
		Str("prefix", cmd.Prefix).
		Msg("restoring cache from s3")

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
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	archiveFile := filepath.Join(tmpDir, tkey)

	// download the cache
	archiveSize, err := blobs.Download(ctx, tkey, archiveFile)
	if err != nil {
		return trace.NewError(span, "failed to download cache: %w", err)
	}

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
	err = archive.ExtractFiles(ctx, archiveFileHandle, archiveSize, paths)
	if err != nil {
		return trace.NewError(span, "failed to extract archive: %w", err)
	}

	// TODO: check if the cache entry is a fallback

	fmt.Println("true") // write to stdout

	return nil
}
