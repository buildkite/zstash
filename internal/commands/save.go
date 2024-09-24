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
	"github.com/klauspost/compress/zstd"
	"github.com/mholt/archiver/v4"

	"github.com/buildkite/zstash/internal/trace"
	"github.com/buildkite/zstash/pkg/key"
	"github.com/buildkite/zstash/pkg/paths"
	"github.com/buildkite/zstash/pkg/store"
)

type SaveCmd struct {
	Key                string   `flag:"key" help:"Key to save, this can be a template string."`
	LocalCachePath     string   `flag:"local-cache-path" help:"Local cache path." env:"LOCAL_CACHE_PATH" required:"true"`
	RemoteCacheURL     string   `flag:"remote-cache-url" help:"Remote cache URL." env:"REMOTE_CACHE_URL"`
	ExpiresInSecs      int64    `flag:"expires-in-secs" help:"Expires in seconds." default:"86400"`
	EncoderConcurrency int      `flag:"encoder-concurrency" help:"Zstd Encoder concurrency." default:"8"`
	UseAccelerate      bool     `flag:"use-accelerate" help:"Use S3 accelerate."`
	Paths              []string `arg:"" name:"path" help:"Paths to remove." type:"path"`
}

func (cmd *SaveCmd) Run(ctx context.Context, globals *Globals) error {
	ctx, span := trace.Start(ctx, "SaveCmdRun")
	defer span.End()

	key, err := key.Resolve(cmd.Key)
	if err != nil {
		return fmt.Errorf("failed to resolve key: %w", err)
	}

	log.Printf("Saving key=%s", key)

	format := archiver.CompressedArchive{
		Compression: archiver.Zstd{
			EncoderOptions: []zstd.EOption{
				zstd.WithEncoderConcurrency(cmd.EncoderConcurrency),
			},
		},
		Archival: archiver.Tar{},
	}

	files, err := buildFilesFromDisk(ctx, cmd.Paths)
	if err != nil {
		return fmt.Errorf("failed to build files from disk: %w", err)
	}

	outputPath := buildOutputPath(cmd.LocalCachePath, key, format)

	log.Printf("Saving outputPath=%s", outputPath)

	err = os.MkdirAll(filepath.Dir(outputPath), 0755)
	if err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	sha256sum, err := saveArchive(ctx, format, files, outputPath)
	if err != nil {
		return fmt.Errorf("failed to save archive: %w", err)
	}

	if cmd.RemoteCacheURL != "" {
		log.Printf("Uploading to remote cache url=%s expires-in-secs=%d", cmd.RemoteCacheURL, cmd.ExpiresInSecs)

		remoteURL, err := url.JoinPath(cmd.RemoteCacheURL, fmt.Sprintf("%s%s", key, format.Name()))
		if err != nil {
			return fmt.Errorf("failed to build remote url: %w", err)
		}

		st, err := store.NewS3Store(cmd.UseAccelerate)
		if err != nil {
			return fmt.Errorf("failed to create s3 store: %w", err)
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
