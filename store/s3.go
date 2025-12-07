package store

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/buildkite/zstash/internal/trace"
	"go.opentelemetry.io/otel/attribute"
)

// Options holds configuration for S3Blob and can be constructed from an S3 URL in a similar way to gocloud.dev
// Example S3 URLs:
//
//	s3://my-bucket
//	s3://my-bucket/prefix
//	s3://my-bucket?region=us-east-1
//	s3://my-bucket/prefix?region=us-east-1&endpoint=http://localhost:9000&use_path_style=true
type Options struct {
	S3Endpoint   string
	Bucket       string
	Region       string
	Prefix       string
	UsePathStyle bool
}

func OptionsFromURL(s3url string) (*Options, error) {
	u, err := url.Parse(s3url)
	if err != nil {
		return nil, fmt.Errorf("failed to parse S3 URL: %w", err)
	}

	// check the scheme is s3
	if u.Scheme != "s3" {
		return nil, fmt.Errorf("invalid S3 URL scheme %q: must be s3", u.Scheme)
	}

	opts := &Options{
		Bucket: u.Hostname(),
		Prefix: strings.Trim(u.Path, "/"),
		// Region and S3Endpoint can be set via query parameters if needed
		Region:     u.Query().Get("region"),
		S3Endpoint: u.Query().Get("endpoint"),
	}

	if opts.Region == "" {
		opts.Region = "us-east-1"
	}

	if u.Query().Get("use_path_style") == "true" {
		opts.UsePathStyle = true
	}

	return opts, nil
}

// S3Blob implements the Blob interface using AWS S3
type S3Blob struct {
	client     *s3.Client
	uploader   *manager.Uploader
	downloader *manager.Downloader
	bucketName string
	prefix     string
}

// NewS3Blob creates a new S3Blob instance using an S3 URL and prefix
func NewS3Blob(ctx context.Context, s3url string) (*S3Blob, error) {
	opts, err := OptionsFromURL(s3url)
	if err != nil {
		return nil, fmt.Errorf("failed to parse S3 URL: %w", err)
	}

	// Load the AWS configuration
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	slog.Debug("configured S3 bucket",
		"bucket", opts.Bucket,
		"region", opts.Region,
		"prefix", opts.Prefix,
		"endpoint", opts.S3Endpoint)

	// Create a new S3 client
	client := s3.NewFromConfig(cfg,
		func(o *s3.Options) {
			o.Region = opts.Region
			if opts.UsePathStyle {
				o.UsePathStyle = true
			}

			// used for local testing or custom S3 endpoints
			if opts.S3Endpoint != "" {
				o.BaseEndpoint = aws.String(opts.S3Endpoint)
			}
		})

	// Create the uploader and downloader with default settings
	// Default concurrency is 5, default part size is 5MB for uploads and 5MB for downloads
	uploader := manager.NewUploader(client)
	downloader := manager.NewDownloader(client)

	slog.Debug("configured S3 transfer manager",
		"upload_concurrency", manager.DefaultUploadConcurrency,
		"upload_part_size", manager.DefaultUploadPartSize,
		"download_concurrency", manager.DefaultDownloadConcurrency,
		"download_part_size", manager.DefaultDownloadPartSize,
	)

	return &S3Blob{
		client:     client,
		uploader:   uploader,
		downloader: downloader,
		bucketName: opts.Bucket,
		prefix:     opts.Prefix,
	}, nil
}

// Upload uploads a file to S3 using multipart upload for parallel transfers
func (b *S3Blob) Upload(ctx context.Context, filePath string, key string) (*TransferInfo, error) {
	ctx, span := trace.Start(ctx, "S3Blob.Upload")
	defer span.End()

	start := time.Now()

	// Get the full key with prefix
	fullKey := b.getFullKey(key)

	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer func() {
		_ = file.Close()
	}()

	// stat the file to get its size
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file %s: %w", filePath, err)
	}

	bytesWritten := fileInfo.Size()

	// Calculate expected part count based on file size and default part size
	partCount := int((bytesWritten + manager.DefaultUploadPartSize - 1) / manager.DefaultUploadPartSize)
	if partCount < 1 {
		partCount = 1
	}

	slog.Debug("starting S3 upload",
		"key", fullKey,
		"file_size", bytesWritten,
		"expected_parts", partCount,
		"concurrency", manager.DefaultUploadConcurrency,
	)

	// Upload the file to S3 using the multipart uploader
	result, err := b.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucketName),
		Key:    aws.String(fullKey),
		Body:   file,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upload file to S3: %w", err)
	}

	// For multipart uploads, the part count is calculated from file size
	// For single part uploads (small files), it's 1
	actualPartCount := partCount
	if result.UploadID == "" {
		// Single part upload (file was smaller than part size threshold)
		actualPartCount = 1
	}

	// Extract request ID from the upload result
	requestID := ""
	if result.UploadID != "" {
		requestID = result.UploadID
	}

	duration := time.Since(start)
	averageSpeed := calculateTransferSpeedMBps(bytesWritten, duration)

	slog.Debug("completed S3 upload",
		"key", fullKey,
		"bytes_transferred", bytesWritten,
		"parts_uploaded", actualPartCount,
		"concurrency", manager.DefaultUploadConcurrency,
		"duration", duration,
		"transfer_speed_mbps", fmt.Sprintf("%.2f", averageSpeed),
	)

	span.SetAttributes(
		attribute.Int64("bytes_transferred", bytesWritten),
		attribute.String("transfer_speed", fmt.Sprintf("%.2fMB/s", averageSpeed)),
		attribute.String("request_id", requestID),
		attribute.Int("part_count", actualPartCount),
		attribute.Int("concurrency", manager.DefaultUploadConcurrency),
	)

	return &TransferInfo{
		BytesTransferred: bytesWritten,
		TransferSpeed:    averageSpeed,
		RequestID:        requestID,
		Duration:         duration,
		PartCount:        actualPartCount,
		Concurrency:      manager.DefaultUploadConcurrency,
	}, nil
}

// Download downloads a file from S3 using parallel range requests for large files
func (b *S3Blob) Download(ctx context.Context, key string, destPath string) (*TransferInfo, error) {
	ctx, span := trace.Start(ctx, "S3Blob.Download")
	defer span.End()

	start := time.Now()

	// Get the full key with prefix
	fullKey := b.getFullKey(key)

	// Create the destination file - must support WriteAt for parallel downloads
	destFile, err := os.Create(destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create destination file %s: %w", destPath, err)
	}
	defer func() {
		_ = destFile.Close()
	}()

	slog.Debug("starting S3 download",
		"key", fullKey,
		"concurrency", manager.DefaultDownloadConcurrency,
	)

	// Download the file from S3 using parallel range requests
	bytesWritten, err := b.downloader.Download(ctx, destFile, &s3.GetObjectInput{
		Bucket: aws.String(b.bucketName),
		Key:    aws.String(fullKey),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download file from S3: %w", err)
	}

	// Calculate part count based on bytes downloaded and default part size
	partCount := int((bytesWritten + manager.DefaultDownloadPartSize - 1) / manager.DefaultDownloadPartSize)
	if partCount < 1 {
		partCount = 1
	}

	duration := time.Since(start)
	averageSpeed := calculateTransferSpeedMBps(bytesWritten, duration)

	slog.Debug("completed S3 download",
		"key", fullKey,
		"bytes_transferred", bytesWritten,
		"parts_downloaded", partCount,
		"concurrency", manager.DefaultDownloadConcurrency,
		"duration", duration,
		"transfer_speed_mbps", fmt.Sprintf("%.2f", averageSpeed),
	)

	span.SetAttributes(
		attribute.Int64("bytes_transferred", bytesWritten),
		attribute.String("transfer_speed", fmt.Sprintf("%.2fMB/s", averageSpeed)),
		attribute.Int("part_count", partCount),
		attribute.Int("concurrency", manager.DefaultDownloadConcurrency),
	)

	return &TransferInfo{
		BytesTransferred: bytesWritten,
		TransferSpeed:    averageSpeed,
		RequestID:        "", // Download doesn't return a single request ID for parallel downloads
		Duration:         duration,
		PartCount:        partCount,
		Concurrency:      manager.DefaultDownloadConcurrency,
	}, nil
}

// getFullKey combines the prefix with the key
func (b *S3Blob) getFullKey(key string) string {
	// Remove leading slash from key if present
	key = strings.TrimPrefix(key, "/")
	// Combine prefix and key
	return path.Join(b.prefix, key)
}
