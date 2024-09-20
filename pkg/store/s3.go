package store

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
)

type S3Store struct {
	client *s3.Client
}

func NewS3Store() (*S3Store, error) {
	sdkConfig, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return nil, fmt.Errorf("failed to load sdk config: %w", err)
	}

	// Create an Amazon S3 service client
	client := s3.NewFromConfig(sdkConfig)

	return &S3Store{client: client}, nil
}

func (s *S3Store) Download(ctx context.Context, remoteCacheURL, path string) error {
	u, err := url.Parse(remoteCacheURL)
	if err != nil {
		return fmt.Errorf("failed to parse remote cache url=%s", remoteCacheURL)
	}

	remotePath, err := url.JoinPath(u.Path, filepath.Base(path))
	if err != nil {
		return fmt.Errorf("failed to join path: %w", err)
	}

	getObj, err := s.client.GetObject(ctx, &s3.GetObjectInput{
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

func (s *S3Store) Upload(ctx context.Context, remoteCacheURL, path string, expiresInSecs int64) error {
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
