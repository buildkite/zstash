package commands

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/klauspost/compress/zstd"
	"github.com/mholt/archiver/v4"

	"github.com/buildkite/zstash/pkg/key"
	"github.com/buildkite/zstash/pkg/store"
)

type SaveCmd struct {
	Key                string   `flag:"key" help:"Key to save, this can be a template string."`
	LocalCachePath     string   `flag:"local-cache-path" help:"Local cache path." env:"LOCAL_CACHE_PATH" required:"true"`
	RemoteCacheURL     string   `flag:"remote-cache-url" help:"Remote cache URL." env:"REMOTE_CACHE_URL"`
	ExpiresInSecs      int64    `flag:"expires-in-secs" help:"Expires in seconds." default:"86400"`
	EncoderConcurrency int      `flag:"encoder-concurrency" help:"Zstd Encoder concurrency." default:"8"`
	Paths              []string `arg:"" name:"path" help:"Paths to remove." type:"path"`
}

func (cmd *SaveCmd) Run(ctx context.Context, globals *Globals) error {
	key, err := key.Resolve(cmd.Key)
	if err != nil {
		return fmt.Errorf("failed to resolve key: %w", err)
	}

	format := archiver.CompressedArchive{
		Compression: archiver.Zstd{
			EncoderOptions: []zstd.EOption{
				zstd.WithEncoderConcurrency(cmd.EncoderConcurrency),
			},
		},
		Archival: archiver.Tar{},
	}

	files, err := buildFilesFromDisk(cmd.Paths)
	if err != nil {
		return fmt.Errorf("failed to build files from disk: %w", err)
	}

	outputPath := buildOutputPath(cmd.LocalCachePath, key, format)

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

		err = store.Upload(ctx, cmd.RemoteCacheURL, outputPath, sha256sum, cmd.ExpiresInSecs)
		if err != nil {
			return fmt.Errorf("failed to upload to remote cache: %w", err)
		}
	}

	return nil
}

func buildFilesFromDisk(paths []string) ([]archiver.File, error) {

	start := time.Now()

	fm := map[string]string{}

	for _, path := range paths {
		fm[path] = "" // TODO: add option to override the path in the archive
	}

	files, err := archiver.FilesFromDisk(nil, fm)
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
