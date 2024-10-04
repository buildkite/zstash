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
	LocalCachePath string   `flag:"local-cache-path" help:"Local cache path." env:"LOCAL_CACHE_PATH" default:"/tmp"`
	RestorePath    string   `flag:"restore-path" help:"Path to restore." default:"." env:"RESTORE_PATH"`
	RemoteCacheURL string   `flag:"remote-cache-url" help:"Remote cache URL." env:"REMOTE_CACHE_URL"`
	Store          string   `flag:"store" help:"store used to upload / download, either s3 or artifact" enum:"s3,artifact" default:"s3"`
	Format         string   `flag:"format" help:"the format of the archive" enum:"zip,tar.zstd" default:"zip"`
	UseAccelerate  bool     `flag:"use-accelerate" help:"Use S3 accelerate."`
	Paths          []string `arg:"" name:"path" help:"Paths within the cache archive to restore to the restore path."`
}

func (cmd *RestoreCmd) Run(ctx context.Context, globals *Globals) error {
	ctx, span := trace.Start(ctx, "RestoreCmdRun")
	defer span.End()

	log.Printf("Restore version=%s", globals.Version)

	format, err := archiveFormat(cmd.Format)
	if err != nil {
		return fmt.Errorf("failed to get archive format: %w", err)
	}

	key, err := key.Resolve(cmd.Key, cmd.Paths)
	if err != nil {
		return fmt.Errorf("failed to resolve key: %w", err)
	}

	span.SetAttributes(
		attribute.String("key", cmd.Key),
		attribute.String("resolved_key", key),
		attribute.String("store", cmd.Store),
	)
	log.Printf("Restore key=%s", key)

	var (
		st        store.Store
		remoteURL string
	)

	switch cmd.Store {
	case "s3":
		remoteURL = cmd.RemoteCacheURL
		st, err = store.NewS3Store(cmd.UseAccelerate)
	case "artifact":
		st, err = store.NewArtifactStore()
	}
	if err != nil {
		return fmt.Errorf("failed to create store: %w", err)
	}

	outputPath := buildOutputPath(cmd.LocalCachePath, key, format)

	remoteURL, err = url.JoinPath(remoteURL, fmt.Sprintf("%s%s", key, format.Name()))
	if err != nil {
		return fmt.Errorf("failed to build remote url: %w", err)
	}

	log.Printf("Download url=%s", remoteURL)

	// check if the cache exists in the remote cache
	_, ok, err := st.Exists(ctx, remoteURL, key)
	if err != nil {
		return fmt.Errorf("failed to check if cache exists: %w", err)
	}

	if !ok {
		span.SetAttributes(
			attribute.Bool("cache.remote.hit", false),
		)
		log.Printf("CACHE REMOTE MISS ❌ key not found in remote cache")
		return nil
	}

	// is the cache already in the local cache?
	if _, err := os.Stat(outputPath); errors.Is(err, os.ErrNotExist) {

		// we didn't find the cache in the local cache, so we need to download it
		span.SetAttributes(
			attribute.Bool("cache.remote.hit", true),
			attribute.Bool("cache.local.hit", false),
		)

		err = st.Download(ctx, remoteURL, outputPath, "") // we don't have a sha256sum
		if err != nil {
			return fmt.Errorf("failed to download cache file: %w", err)
		}

		// we downloaded the cache from the remote cache and can now restore it
		log.Printf("Downloaded cache from remote cache to local cache=%s", outputPath)

		log.Printf("CACHE REMOTE HIT ✅ key not found in local cache")

	} else {
		// we found the cache in the local cache, so we can restore it
		span.SetAttributes(
			attribute.Bool("cache.remote.hit", true),
			attribute.Bool("cache.local.hit", true),
		)
		log.Printf("CACHE LOCAL HIT ✅ cache found in local cache")
	}

	stats := &FileStats{}
	archiveSize, err := extractArchive(ctx, format, outputPath, cmd.Paths, fileHandler(cmd.RestorePath, globals.Debug, stats))
	if err != nil {
		return fmt.Errorf("failed to extract archive: %w", err)
	}

	span.SetAttributes(
		attribute.Int("archive.files", stats.Files),
		attribute.Int("archive.dirs", stats.Dirs),
		attribute.Int("archive.dirs", stats.Links),
		attribute.Int64("archive.files.size", stats.Size),
		attribute.Int64("archive.size", archiveSize),
	)

	log.Printf("Restored archive files=%d dirs=%d links=%d files_total_size=%d archive_size=%d", stats.Files, stats.Dirs, stats.Links, stats.Size, archiveSize)

	return nil
}

func extractArchive(ctx context.Context, format archiver.CompressedArchive, outputPath string, paths []string, handler archiver.FileHandler) (int64, error) {
	ctx, span := trace.Start(ctx, "extractArchive")
	defer span.End()

	f, err := os.Open(outputPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	finfo, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("failed to stat file: %w", err)
	}

	err = format.Extract(ctx, f, paths, handler)
	if err != nil {
		return 0, fmt.Errorf("failed to archive: %w", err)
	}

	return finfo.Size(), nil
}

type FileStats struct {
	Files int
	Links int
	Dirs  int
	Size  int64
}

func fileHandler(restorePath string, debug bool, stats *FileStats) archiver.FileHandler {
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

			stats.Dirs++

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

			// also count links
			stats.Links++

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

		n, err := io.Copy(tf, f)
		if err != nil {
			return err
		}

		stats.Files++
		stats.Size += n

		// defer func(start time.Time, path string) {
		// 	log.Printf("Extracted path=%s in=%s", path, time.Since(start))
		// }(start, path)

		return nil
	}
}
