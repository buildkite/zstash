package store

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"

	"github.com/buildkite/zstash/internal/trace"
)

// S3Store is a store that uses the S3 API with the s3:// scheme, it uses async multipart upload API and download.
type S3Store struct {
	client *s3.Client
}

func NewS3Store() (*S3Store, error) {
	sdkConfig, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return nil, fmt.Errorf("failed to load sdk config: %w", err)
	}

	// instrument all aws clients
	otelaws.AppendMiddlewares(&sdkConfig.APIOptions)

	// Create an Amazon S3 service client
	client := s3.NewFromConfig(sdkConfig)

	return &S3Store{client: client}, nil
}

func (s *S3Store) Download(ctx context.Context, remoteCacheURL, path, sha256sum string) error {
	ctx, span := trace.Start(ctx, "S3Store.Download")
	defer span.End()

	u, err := url.Parse(remoteCacheURL)
	if err != nil {
		return fmt.Errorf("failed to parse remote cache url=%s", remoteCacheURL)
	}

	log.Printf("Downloading from s3 bucket to file url=%s path=%s", remoteCacheURL, path)

	err = os.MkdirAll(filepath.Dir(path), 0755)
	if err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	downloadFile, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}

	defer downloadFile.Close()

	downloader := manager.NewDownloader(s.client)
	n, err := downloader.Download(ctx, downloadFile, &s3.GetObjectInput{
		Bucket: aws.String(u.Host),
		Key:    aws.String(u.Path),
	})
	if err != nil {
		var notFoundErr *types.NoSuchKey
		if errors.As(err, &notFoundErr) {
			return ErrNotFound
		}
	}

	log.Printf("Downloaded from s3 bucket url=%s size=%d", remoteCacheURL, n)

	return nil
}

func (s *S3Store) Upload(ctx context.Context, remoteCacheURL, path, sha256sum string, expiresInSecs int64) error {
	ctx, span := trace.Start(ctx, "S3Store.Upload")
	defer span.End()

	start := time.Now()

	u, err := url.Parse(remoteCacheURL)
	if err != nil {
		return fmt.Errorf("failed to parse remote cache url=%s", remoteCacheURL)
	}

	rawSum, err := hex.DecodeString(sha256sum)
	if err != nil {
		return fmt.Errorf("failed to decode sha256sum: %w", err)
	}

	base64Sum := base64.StdEncoding.EncodeToString(rawSum)

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}

	headRes, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket:       aws.String(u.Host),
		Key:          aws.String(u.Path),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		var notFoundErr *types.NotFound
		if !errors.As(err, &notFoundErr) {
			return fmt.Errorf("failed to head object: %w", err)
		}
	}
	if err == nil {
		log.Printf("Headed s3 bucket url=%s sha256sum=%s", remoteCacheURL, aws.ToString(headRes.ChecksumSHA256))

		if aws.ToString(headRes.ChecksumSHA256) == base64Sum {
			log.Printf("File already exists in s3 bucket url=%s sha256sum=%s", remoteCacheURL, sha256sum)
			return nil
		}
	}

	uploader := manager.NewUploader(s.client)
	uploadRes, err := uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:         aws.String(u.Host),
		Key:            aws.String(u.Path),
		Body:           f,
		Expires:        aws.Time(time.Now().Add(time.Duration(expiresInSecs) * time.Second)),
		ChecksumSHA256: aws.String(base64Sum),
	})
	if err != nil {
		return fmt.Errorf("failed to upload file to s3: %w", err)
	}

	log.Printf("Uploaded to s3 bucket url=%s etag=%s sha256sum=%s duration=%s", remoteCacheURL, aws.ToString(uploadRes.ETag), sha256sum, time.Since(start))

	return nil
}
