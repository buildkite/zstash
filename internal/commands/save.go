package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

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
	Paths              []string `arg:"" name:"path" help:"Paths to remove." type:"path"`
	EncoderConcurrency int      `flag:"encoder-concurrency" help:"Encoder concurrency." default:"8"`
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

	fm := map[string]string{}

	for _, path := range cmd.Paths {
		fm[path] = "" // TODO: add option to override the path in the archive
	}

	files, err := archiver.FilesFromDisk(nil, fm)
	if err != nil {
		return fmt.Errorf("failed to get files from disk: %w", err)
	}

	if globals.Debug {
		for _, file := range files {
			log.Printf("Adding %s", file.NameInArchive)
		}
	}

	outputPath := filepath.Join(cmd.LocalCachePath, fmt.Sprintf("%s%s%s", key, format.Archival.Name(), format.Compression.Name()))

	log.Printf("Saving to path=%s", outputPath)

	err = os.MkdirAll(filepath.Dir(outputPath), 0755)
	if err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// create the output file we'll write to
	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer out.Close()

	err = format.Archive(ctx, out, files)
	if err != nil {
		return fmt.Errorf("failed to archive: %w", err)
	}

	finfo, err := out.Stat()
	if err != nil {
		return fmt.Errorf("failed to close output file: %w", err)
	}

	log.Printf("Wrote archive path=%s size=%s", out.Name(), humanize.Bytes(uint64(finfo.Size())))

	if cmd.RemoteCacheURL != "" {
		log.Printf("Uploading to remote cache url=%s expires-in-secs=%d", cmd.RemoteCacheURL, cmd.ExpiresInSecs)

		err = store.Upload(ctx, cmd.RemoteCacheURL, outputPath, cmd.ExpiresInSecs)
		if err != nil {
			return fmt.Errorf("failed to upload to remote cache: %w", err)
		}
	}

	return nil
}
