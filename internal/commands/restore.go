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
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/mholt/archiver/v4"
)

type RestoreCmd struct {
	Key            string   `flag:"key" help:"Key to restore."`
	LocalCachePath string   `flag:"local-cache-path" help:"Local cache path." env:"LOCAL_CACHE_PATH"`
	RestorePath    string   `flag:"restore-path" help:"Path to restore." default:"." env:"RESTORE_PATH"`
	RemoteCacheURL string   `flag:"remote-cache-url" help:"Remote cache URL." env:"REMOTE_CACHE_URL"`
	Paths          []string `arg:"" name:"path" help:"Paths within the cache archive to restore to the restore path."`
}

func (cmd *RestoreCmd) Run(ctx context.Context, globals *Globals) error {
	format := archiver.CompressedArchive{
		Compression: archiver.Zstd{},
		Archival:    archiver.Tar{},
	}

	outputPath := filepath.Join(cmd.LocalCachePath, fmt.Sprintf("%s%s%s", cmd.Key, format.Archival.Name(), format.Compression.Name()))

	// does the cache exist locally?
	if _, err := os.Stat(outputPath); errors.Is(err, os.ErrNotExist) {

		if cmd.RemoteCacheURL == "" {
			return nil // there was no fall back cache key so we can't restore
		}

		err = downloadFile(ctx, cmd.RemoteCacheURL, outputPath)
		if err != nil {
			if globals.Debug {
				log.Printf("Failed to download file: %v", err)
			}
			return nil // there was no fall back cache key so we can't restore
		}

	}

	log.Printf("Extracting to archive=%s paths=%q", outputPath, cmd.Paths)

	f, err := os.Open(outputPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	handler := func(ctx context.Context, file archiver.File) error {
		if globals.Debug {
			log.Printf("Extracting name=%s dir=%v", file.NameInArchive, file.IsDir())
		}

		path := filepath.Join(cmd.RestorePath, file.NameInArchive)

		if file.IsDir() {
			err := os.MkdirAll(path, file.Mode())
			if err != nil {
				log.Printf("Failed to create directory: %v", err)
			}

			if globals.Debug {
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

			if globals.Debug {
				log.Printf("Creating link name=%s target=%s", path, file.LinkTarget)
			}

			return nil // we are done
		}

		start := time.Now()

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

		defer func(start time.Time, path string) {
			log.Printf("Extracted path=%s in=%s", path, time.Since(start))
		}(start, path)

		return nil
	}

	err = format.Extract(ctx, f, cmd.Paths, handler)
	if err != nil {
		return fmt.Errorf("failed to archive: %w", err)
	}

	return nil
}

func downloadFile(ctx context.Context, remoteCacheURL, path string) error {

	u, err := url.Parse(remoteCacheURL)
	if err != nil {
		return fmt.Errorf("failed to parse remote cache url=%s", remoteCacheURL)
	}

	sdkConfig, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return fmt.Errorf("failed to load sdk config: %w", err)
	}

	// Create an Amazon S3 service client
	client := s3.NewFromConfig(sdkConfig)

	remotePath, err := url.JoinPath(u.Path, filepath.Base(path))
	if err != nil {
		return fmt.Errorf("failed to join path: %w", err)
	}

	getObj, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(u.Host),
		Key:    aws.String(remotePath),
	})
	if err != nil {
		return fmt.Errorf("failed to get object: %w", err)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}

	defer f.Close()

	n, err := f.ReadFrom(getObj.Body)
	if err != nil {
		return fmt.Errorf("failed to read from body: %w", err)
	}

	log.Printf("Downloaded from s3 bucket=%s key=%s size=%d", u.Host, remotePath, n)

	return nil
}
