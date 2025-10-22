package store

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/buildkite/zstash/internal/trace"
	"github.com/rs/zerolog/log"
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

	log.Debug().
		Str("bucket", opts.Bucket).
		Str("region", opts.Region).
		Str("prefix", opts.Prefix).
		Str("endpoint", opts.S3Endpoint).
		Msg("configured S3 bucket")

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

	return &S3Blob{
		client:     client,
		bucketName: opts.Bucket,
		prefix:     opts.Prefix,
	}, nil
}

// Upload uploads a file to S3
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

	// Upload the file to S3
	result, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucketName),
		Key:    aws.String(fullKey),
		Body:   file,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upload file to S3: %w", err)
	}

	requestID, _ := middleware.GetRequestIDMetadata(result.ResultMetadata)

	bytesWritten := fileInfo.Size()

	averageSpeed := calculateTransferSpeedMBps(bytesWritten, time.Since(start))

	span.SetAttributes(
		attribute.Int64("bytes_transferred", bytesWritten),
		attribute.String("transfer_speed", fmt.Sprintf("%.2fMB/s", averageSpeed)),
		attribute.String("request_id", requestID),
	)

	return &TransferInfo{
		BytesTransferred: bytesWritten,
		TransferSpeed:    averageSpeed,
		RequestID:        requestID,
		Duration:         time.Since(start),
	}, nil
}

// Download downloads a file from S3
func (b *S3Blob) Download(ctx context.Context, key string, destPath string) (*TransferInfo, error) {
	ctx, span := trace.Start(ctx, "S3Blob.Download")
	defer span.End()

	start := time.Now()

	// Get the full key with prefix
	fullKey := b.getFullKey(key)

	// Get the object from S3
	result, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucketName),
		Key:    aws.String(fullKey),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download file from S3: %w", err)
	}
	defer func() {
		_ = result.Body.Close()
	}()

	requestID, _ := middleware.GetRequestIDMetadata(result.ResultMetadata)

	// Create the destination file
	destFile, err := os.Create(destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create destination file %s: %w", destPath, err)
	}
	defer func() {
		_ = destFile.Close()
	}()

	// Write the S3 object to the file
	bytesWritten, err := destFile.ReadFrom(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to write file contents: %w", err)
	}

	averageSpeed := calculateTransferSpeedMBps(bytesWritten, time.Since(start))

	span.SetAttributes(
		attribute.Int64("bytes_transferred", bytesWritten),
		attribute.String("transfer_speed", fmt.Sprintf("%.2fMB/s", averageSpeed)),
	)

	return &TransferInfo{
		BytesTransferred: bytesWritten,
		TransferSpeed:    averageSpeed,
		RequestID:        requestID,
		Duration:         time.Since(start),
	}, nil
}

// getFullKey combines the prefix with the key
func (b *S3Blob) getFullKey(key string) string {
	// Remove leading slash from key if present
	key = strings.TrimPrefix(key, "/")
	// Combine prefix and key
	return path.Join(b.prefix, key)
}
