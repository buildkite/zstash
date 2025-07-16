package store

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/tracing/smithyoteltracing"
	"github.com/buildkite/zstash/internal/trace"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// S3Blob implements the Blob interface using AWS S3
type S3Blob struct {
	client     *s3.Client
	bucketName string
	prefix     string
}

// NewS3Blob creates a new S3Blob instance using an S3 URL and prefix
func NewS3Blob(ctx context.Context, s3url, prefix, s3Endpoint string) (*S3Blob, error) {
	// Parse the S3 URL to extract the bucket name
	// s3url format: s3://bucket-name or https://bucket-name.s3.region.amazonaws.com
	bucketName, err := extractBucketName(s3url)
	if err != nil {
		return nil, err
	}

	// Normalize the prefix to ensure it has the correct format
	normalizedPrefix := normalizePrefix(prefix)

	// Load the AWS configuration
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create a new S3 client
	client := s3.NewFromConfig(cfg,
		func(o *s3.Options) {
			o.TracerProvider = smithyoteltracing.Adapt(otel.GetTracerProvider())

			// used for local testing or custom S3 endpoints
			if s3Endpoint != "" {
				o.BaseEndpoint = aws.String(s3Endpoint)
				o.Region = "us-east-1" // Default region, can be overridden
				o.UsePathStyle = true  // Use path-style requests for S3
			}
		})

	return &S3Blob{
		client:     client,
		bucketName: bucketName,
		prefix:     normalizedPrefix,
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

// extractBucketName extracts the bucket name from an S3 URL
func extractBucketName(s3url string) (string, error) {
	if strings.HasPrefix(s3url, "s3://") {
		// Format: s3://bucket-name
		parts := strings.SplitN(s3url[5:], "/", 2)
		return parts[0], nil
	} else if strings.HasPrefix(s3url, "https://") {
		// Format: https://bucket-name.s3.region.amazonaws.com
		host := strings.TrimPrefix(s3url, "https://")
		if strings.Contains(host, ".s3.") && strings.Contains(host, ".amazonaws.com") {
			parts := strings.Split(host, ".")
			if len(parts) > 0 {
				return parts[0], nil
			}
		}
	}
	return "", fmt.Errorf("invalid S3 URL format: %s", s3url)
}

// normalizePrefix ensures the prefix has the correct format
func normalizePrefix(prefix string) string {
	// Remove leading slash if present
	prefix = strings.TrimPrefix(prefix, "/")
	// Add trailing slash if not empty and doesn't have one
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return prefix
}

// getFullKey combines the prefix with the key
func (b *S3Blob) getFullKey(key string) string {
	// Remove leading slash from key if present
	key = strings.TrimPrefix(key, "/")
	// Combine prefix and key
	return path.Join(b.prefix, key)
}

// calculateTransferSpeedMBps calculates transfer speed in MB/s (decimal megabytes)
// using the formula: bytes / duration_in_seconds / 1,000,000
func calculateTransferSpeedMBps(bytes int64, duration time.Duration) float64 {
	return float64(bytes) / duration.Seconds() / 1000 / 1000
}
