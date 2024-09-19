package commands

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/dustin/go-humanize"
	"github.com/mholt/archiver/v4"
)

type SaveCmd struct {
	Key            string   `flag:"key" help:"Key to save."`
	LocalCachePath string   `flag:"local-cache-path" help:"Local cache path." env:"LOCAL_CACHE_PATH" required:"true"`
	RemoteCacheURL string   `flag:"remote-cache-url" help:"Remote cache URL." env:"REMOTE_CACHE_URL"`
	ExpiresInSecs  int64    `flag:"expires-in-secs" help:"Expires in seconds." default:"86400"`
	Paths          []string `arg:"" name:"path" help:"Paths to remove." type:"path"`
	MultiThreaded  bool     `flag:"multi-threaded" help:"Enable multi-threaded compression." default:"true"`
}

func (cmd *SaveCmd) Run(ctx context.Context, globals *Globals) error {

	format := archiver.CompressedArchive{
		Compression: archiver.Zstd{},
		Archival:    archiver.Tar{},
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

	outputPath := filepath.Join(cmd.LocalCachePath, fmt.Sprintf("%s%s%s", cmd.Key, format.Archival.Name(), format.Compression.Name()))

	log.Printf("Saving to path=%s", outputPath)

	// create the output file we'll write to
	out, err := os.Create(outputPath)
	if err != nil {
		return err
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

		err = uploadToRemoteCache(ctx, cmd.RemoteCacheURL, outputPath, cmd.ExpiresInSecs)
		if err != nil {
			return fmt.Errorf("failed to upload to remote cache: %w", err)
		}
	}

	return nil
}

func uploadToRemoteCache(ctx context.Context, remoteCacheURL, path string, expiresInSecs int64) error {
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

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}

	remotePath, err := url.JoinPath(u.Path, filepath.Base(path))
	if err != nil {
		return fmt.Errorf("failed to join path: %w", err)
	}

	// Upload the file to the S3 bucket
	putRes, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:  aws.String(u.Host),
		Key:     aws.String(remotePath),
		Body:    f,
		Expires: aws.Time(time.Now().Add(time.Duration(expiresInSecs) * time.Second)),
	})
	if err != nil {
		return fmt.Errorf("failed to upload file to s3: %w", err)
	}

	log.Printf("Uploaded to s3 bucket=%s key=%s etag=%s", u.Host, remotePath, aws.ToString(putRes.ETag))

	return nil
}
