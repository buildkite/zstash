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
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
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

func (s *S3Store) Download(ctx context.Context, remoteCacheURL, path, sha256sum string) error {
	u, err := url.Parse(remoteCacheURL)
	if err != nil {
		return fmt.Errorf("failed to parse remote cache url=%s", remoteCacheURL)
	}

	remotePath, err := url.JoinPath(u.Path, filepath.Base(path))
	if err != nil {
		return fmt.Errorf("failed to join path: %w", err)
	}

	log.Printf("Downloading from s3 bucket=%s key=%s", u.Host, remotePath)

	getObj, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(u.Host),
		Key:    aws.String(remotePath),
	})
	if err != nil {
		var notFoundErr *types.NoSuchKey
		if errors.As(err, &notFoundErr) {
			return ErrNotFound
		}

		return fmt.Errorf("failed to get object: %w", err)
	}

	log.Printf("Saving to path=%s", path)

	f, err := os.Create(path)
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

func (s *S3Store) Upload(ctx context.Context, remoteCacheURL, path, sha256sum string, expiresInSecs int64) error {
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

	remotePath, err := url.JoinPath(u.Path, filepath.Base(path))
	if err != nil {
		return fmt.Errorf("failed to join path: %w", err)
	}

	headRes, err := s.client.HeadObject(context.TODO(), &s3.HeadObjectInput{
		Bucket:       aws.String(u.Host),
		Key:          aws.String(remotePath),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		var notFoundErr *types.NotFound
		if !errors.As(err, &notFoundErr) {
			return fmt.Errorf("failed to head object: %w", err)
		}
	}
	if err == nil {
		log.Printf("Headed s3 bucket=%s key=%s sha256sum=%s", u.Host, remotePath, aws.ToString(headRes.ChecksumSHA256))

		if aws.ToString(headRes.ChecksumSHA256) == base64Sum {
			log.Printf("File already exists in s3 bucket=%s key=%s sha256sum=%s", u.Host, remotePath, sha256sum)
			return nil
		}
	}

	// Upload the file to the S3 bucket
	putRes, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:         aws.String(u.Host),
		Key:            aws.String(remotePath),
		Body:           f,
		Expires:        aws.Time(time.Now().Add(time.Duration(expiresInSecs) * time.Second)),
		ChecksumSHA256: aws.String(base64Sum),
	})
	if err != nil {
		return fmt.Errorf("failed to upload file to s3: %w", err)
	}

	log.Printf("Uploaded to s3 bucket=%s key=%s etag=%s sha256sum=%s duration=%s", u.Host, remotePath, aws.ToString(putRes.ETag), sha256sum, time.Since(start))

	return nil
}
