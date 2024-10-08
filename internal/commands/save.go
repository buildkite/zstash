package commands

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/mholt/archiver/v4"
	"go.opentelemetry.io/otel/attribute"

	"github.com/buildkite/zstash/internal/trace"
	"github.com/buildkite/zstash/pkg/key"
	"github.com/buildkite/zstash/pkg/paths"
	"github.com/buildkite/zstash/pkg/store"
)

type SaveCmd struct {
	Key                string   `flag:"key" help:"Key to save, this can be a template string."`
	LocalCachePath     string   `flag:"local-cache-path" help:"Local cache path." env:"LOCAL_CACHE_PATH" default:"/tmp"`
	RemoteCacheURL     string   `flag:"remote-cache-url" help:"Remote cache URL." env:"REMOTE_CACHE_URL"`
	Store              string   `flag:"store" help:"store used to upload / download, either s3 or artifact" enum:"s3,artifact" default:"s3"`
	ExpiresInSecs      int64    `flag:"expires-in-secs" help:"Expires in seconds." default:"86400"`
	Format             string   `flag:"format" help:"the format of the archive" enum:"zip,tar.zstd" default:"tar.zstd"`
	EncoderConcurrency int      `flag:"encoder-concurrency" help:"Zstd Encoder concurrency." default:"8"`
	UseAccelerate      bool     `flag:"use-accelerate" help:"Use S3 accelerate."`
	Paths              []string `arg:"" name:"path" help:"Paths to remove." type:"path"`
}

func (cmd *SaveCmd) Run(ctx context.Context, globals *Globals) error {
	ctx, span := trace.Start(ctx, "SaveCmdRun")
	defer span.End()

	log.Printf("Save version=%s", globals.Version)

	key, err := key.Resolve(cmd.Key, cmd.Paths)
	if err != nil {
		return fmt.Errorf("failed to resolve key: %w", err)
	}

	log.Printf("Saving key=%s", key)
	span.SetAttributes(
		attribute.String("key", cmd.Key),
		attribute.String("resolved_key", key),
		attribute.String("store", cmd.Store),
		attribute.String("remote_cache_url", cmd.RemoteCacheURL),
	)

	format, err := archiveFormat(cmd.Format)
	if err != nil {
		return fmt.Errorf("failed to get archive format: %w", err)
	}

	files, err := buildFilesFromDisk(ctx, cmd.Paths)
	if err != nil {
		return fmt.Errorf("failed to build files from disk: %w", err)
	}

	outputPath := buildOutputPath(cmd.LocalCachePath, key, format)

	sha256sum, err := saveArchive(ctx, format, files, outputPath)
	if err != nil {
		return fmt.Errorf("failed to save archive: %w", err)
	}

	var (
		st        store.Store
		remoteURL string
	)

	if cmd.RemoteCacheURL != "" || cmd.Store == "artifact" {
		log.Printf("Uploading to remote cache url=%s expires-in-secs=%d", cmd.RemoteCacheURL, cmd.ExpiresInSecs)

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

		remoteURL, err = url.JoinPath(remoteURL, fmt.Sprintf("%s%s", key, format.Name()))
		if err != nil {
			return fmt.Errorf("failed to build remote url: %w", err)
		}

		err = st.Upload(ctx, remoteURL, outputPath, sha256sum, cmd.ExpiresInSecs)
		if err != nil {
			return fmt.Errorf("failed to upload to remote cache: %w", err)
		}
	}

	return nil
}

func buildFilesFromDisk(ctx context.Context, filepaths []string) ([]archiver.File, error) {
	_, span := trace.Start(ctx, "buildFilesFromDisk")
	defer span.End()
	start := time.Now()

	dir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	filenames := map[string]string{}

	for _, p := range filepaths {
		filenames[p] = paths.RelPathCheck(dir, p) // TODO: add option to override the path in the archive
	}

	log.Printf("Built file path mappings for archive paths=%q", filenames)

	files, err := archiver.FilesFromDisk(nil, filenames)
	if err != nil {
		return nil, fmt.Errorf("failed to get files from disk: %w", err)
	}

	log.Printf("Built files from disk in duration=%s", time.Since(start))

	return files, nil
}

func buildOutputPath(localCachePath, key string, format archiver.CompressedArchive) string {
	return filepath.Join(localCachePath, fmt.Sprintf("%s%s", key, format.Name()))
}

func saveArchive(ctx context.Context, format archiver.CompressedArchive, files []archiver.File, outputPath string) (string, error) {
	ctx, span := trace.Start(ctx, "saveArchive")
	defer span.End()

	start := time.Now()

	// if the file already exists, we don't need to re-archive it
	if _, err := os.Stat(outputPath); err == nil {

		log.Printf("Archive already exists path=%s", outputPath)

		out, err := os.Open(outputPath)
		if err != nil {
			return "", fmt.Errorf("failed to open existing archive: %w", err)
		}
		defer out.Close()

		sha256Hash := sha256.New()

		if _, err := io.Copy(sha256Hash, out); err != nil {
			return "", fmt.Errorf("failed to calculate sha256sum: %w", err)
		}

		return fmt.Sprintf("%x", sha256Hash.Sum(nil)), nil
	}

	err := os.MkdirAll(filepath.Dir(outputPath), 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	log.Printf("Creating archive outputPath=%s", outputPath)

	out, err := os.Create(outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to create output file: %w", err)
	}
	defer out.Close()

	sha256Hash := sha256.New()

	multiWriter := io.MultiWriter(out, sha256Hash)

	err = format.Archive(ctx, multiWriter, files)
	if err != nil {
		return "", fmt.Errorf("failed to archive: %w", err)
	}

	finfo, err := out.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to close output file: %w", err)
	}

	log.Printf("Wrote archive path=%s size=%s duration=%s sha256sum=%x", out.Name(), humanize.Bytes(uint64(finfo.Size())), time.Since(start), sha256Hash.Sum(nil))

	return fmt.Sprintf("%x", sha256Hash.Sum(nil)), nil
}
