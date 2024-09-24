package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"

	"github.com/mholt/archiver/v4"
	"go.opentelemetry.io/otel/attribute"

	"github.com/buildkite/zstash/internal/trace"
	"github.com/buildkite/zstash/pkg/key"
	"github.com/buildkite/zstash/pkg/store"
)

type RestoreCmd struct {
	Key            string   `flag:"key" help:"Key to restore."`
	LocalCachePath string   `flag:"local-cache-path" help:"Local cache path." env:"LOCAL_CACHE_PATH"`
	RestorePath    string   `flag:"restore-path" help:"Path to restore." default:"." env:"RESTORE_PATH"`
	RemoteCacheURL string   `flag:"remote-cache-url" help:"Remote cache URL." env:"REMOTE_CACHE_URL"`
	UseAccelerate  bool     `flag:"use-accelerate" help:"Use S3 accelerate."`
	Paths          []string `arg:"" name:"path" help:"Paths within the cache archive to restore to the restore path."`
}

func (cmd *RestoreCmd) Run(ctx context.Context, globals *Globals) error {
	ctx, span := trace.Start(ctx, "RestoreCmdRun")
	defer span.End()

	log.Printf("Restore version=%s", globals.Version)

	format := archiver.CompressedArchive{
		Compression: archiver.Zstd{},
		Archival:    archiver.Tar{},
	}

	key, err := key.Resolve(cmd.Key, cmd.Paths)
	if err != nil {
		return fmt.Errorf("failed to resolve key: %w", err)
	}

	span.SetAttributes(attribute.String("key", cmd.Key))
	log.Printf("Restore key=%s", key)

	outputPath := buildOutputPath(cmd.LocalCachePath, key, format)

	// does the cache exist locally?
	if _, err := os.Stat(outputPath); errors.Is(err, os.ErrNotExist) {

		log.Printf("Cache not found locally, trying to download from remote cache")

		if cmd.RemoteCacheURL == "" {
			return nil // there was no fall back cache key so we can't restore
		}

		remoteURL, err := url.JoinPath(cmd.RemoteCacheURL, fmt.Sprintf("%s%s", key, format.Name()))
		if err != nil {
			return fmt.Errorf("failed to build remote url: %w", err)
		}

		log.Printf("Download url=%s", remoteURL)

		st, err := store.NewS3Store(cmd.UseAccelerate)
		if err != nil {
			return fmt.Errorf("failed to create s3 store: %w", err)
		}

		err = st.Download(ctx, remoteURL, outputPath, "") // we don't have a sha256sum
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				span.SetAttributes(attribute.Bool("cache.hit", false))
				log.Printf("CACHE MISS ❌ no cache found locally, and no cache to download from remote cache")

				return nil // there was no fall back cache key so we
			}

			// we can't restore the cache
			// TODO: we may want to ignore this error and continue if we have a fallback key
			return fmt.Errorf("failed to download cache file: %w", err)
		}

		// we downloaded the cache from the remote cache and can now restore it
		log.Printf("Downloaded cache from remote cache to local cache=%s", outputPath)
	}

	span.SetAttributes(attribute.Bool("cache.hit", true))
	log.Printf("CACHE HIT ✅ to archive=%s paths=%q", outputPath, cmd.Paths)

	return extractArchive(ctx, format, outputPath, cmd.Paths, fileHandler(cmd.RestorePath, globals.Debug))
}

func extractArchive(ctx context.Context, format archiver.CompressedArchive, outputPath string, paths []string, handler archiver.FileHandler) error {
	ctx, span := trace.Start(ctx, "extractArchive")
	defer span.End()

	f, err := os.Open(outputPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	err = format.Extract(ctx, f, paths, handler)
	if err != nil {
		return fmt.Errorf("failed to archive: %w", err)
	}

	return nil
}

func fileHandler(restorePath string, debug bool) archiver.FileHandler {

	return func(ctx context.Context, file archiver.File) error {
		if debug {
			log.Printf("Extracting name=%s dir=%v", file.NameInArchive, file.IsDir())
		}

		path := filepath.Join(restorePath, file.NameInArchive)

		if file.IsDir() {
			err := os.MkdirAll(path, file.Mode())
			if err != nil {
				log.Printf("Failed to create directory: %v", err)
			}

			if debug {
				log.Printf("Creating directory name=%s", path)
			}

			return nil // we are done
		}

		if file.LinkTarget != "" {
			err := os.Symlink(file.LinkTarget, path)
			if err != nil {
				// if the file already exists, we are good, otherwise we have a problem
				if !errors.Is(err, os.ErrExist) {
					return fmt.Errorf("failed to create symlink: %w", err)
				}
			}

			if debug {
				log.Printf("Creating link name=%s target=%s", path, file.LinkTarget)
			}

			return nil // we are done
		}

		// start := time.Now()

		f, err := file.Open()
		if err != nil {
			return fmt.Errorf("failed to open file: %w", err)
		}
		defer f.Close()

		tf, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, file.Mode())
		if err != nil {
			return fmt.Errorf("failed to open file: %w", err)
		}
		defer tf.Close()

		_, err = io.Copy(tf, f)
		if err != nil {
			return err
		}

		// defer func(start time.Time, path string) {
		// 	log.Printf("Extracted path=%s in=%s", path, time.Since(start))
		// }(start, path)

		return nil
	}
}
