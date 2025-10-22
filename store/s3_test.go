package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptionsFromURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		want        *Options
		wantErr     bool
		errContains string
	}{
		{
			name: "simple s3 bucket",
			url:  "s3://my-bucket",
			want: &Options{
				Bucket:       "my-bucket",
				Region:       "us-east-1", // default
				Prefix:       "",
				S3Endpoint:   "",
				UsePathStyle: false,
			},
			wantErr: false,
		},
		{
			name: "s3 bucket with prefix",
			url:  "s3://my-bucket/cache/artifacts",
			want: &Options{
				Bucket:       "my-bucket",
				Region:       "us-east-1",
				Prefix:       "cache/artifacts",
				S3Endpoint:   "",
				UsePathStyle: false,
			},
			wantErr: false,
		},
		{
			name: "s3 bucket with trailing slash in prefix",
			url:  "s3://my-bucket/cache/artifacts/",
			want: &Options{
				Bucket:       "my-bucket",
				Region:       "us-east-1",
				Prefix:       "cache/artifacts",
				S3Endpoint:   "",
				UsePathStyle: false,
			},
			wantErr: false,
		},
		{
			name: "s3 bucket with region query param",
			url:  "s3://my-bucket?region=us-west-2",
			want: &Options{
				Bucket:       "my-bucket",
				Region:       "us-west-2",
				Prefix:       "",
				S3Endpoint:   "",
				UsePathStyle: false,
			},
			wantErr: false,
		},
		{
			name: "s3 bucket with prefix and region",
			url:  "s3://my-bucket/some/prefix?region=eu-central-1",
			want: &Options{
				Bucket:       "my-bucket",
				Region:       "eu-central-1",
				Prefix:       "some/prefix",
				S3Endpoint:   "",
				UsePathStyle: false,
			},
			wantErr: false,
		},
		{
			name: "s3 bucket with custom endpoint for local testing",
			url:  "s3://my-bucket?endpoint=http://localhost:9000",
			want: &Options{
				Bucket:       "my-bucket",
				Region:       "us-east-1",
				Prefix:       "",
				S3Endpoint:   "http://localhost:9000",
				UsePathStyle: false,
			},
			wantErr: false,
		},
		{
			name: "s3 bucket with path style access",
			url:  "s3://my-bucket?use_path_style=true",
			want: &Options{
				Bucket:       "my-bucket",
				Region:       "us-east-1",
				Prefix:       "",
				S3Endpoint:   "",
				UsePathStyle: true,
			},
			wantErr: false,
		},
		{
			name: "s3 bucket with all options",
			url:  "s3://my-bucket/prefix/path?region=ap-southeast-2&endpoint=http://localhost:9000&use_path_style=true",
			want: &Options{
				Bucket:       "my-bucket",
				Region:       "ap-southeast-2",
				Prefix:       "prefix/path",
				S3Endpoint:   "http://localhost:9000",
				UsePathStyle: true,
			},
			wantErr: false,
		},
		{
			name: "use_path_style=false is ignored",
			url:  "s3://my-bucket?use_path_style=false",
			want: &Options{
				Bucket:       "my-bucket",
				Region:       "us-east-1",
				Prefix:       "",
				S3Endpoint:   "",
				UsePathStyle: false,
			},
			wantErr: false,
		},
		{
			name:        "invalid URL",
			url:         "://invalid",
			want:        nil,
			wantErr:     true,
			errContains: "failed to parse S3 URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := OptionsFromURL(tt.url)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want.Bucket, got.Bucket, "Bucket mismatch")
			assert.Equal(t, tt.want.Region, got.Region, "Region mismatch")
			assert.Equal(t, tt.want.Prefix, got.Prefix, "Prefix mismatch")
			assert.Equal(t, tt.want.S3Endpoint, got.S3Endpoint, "S3Endpoint mismatch")
			assert.Equal(t, tt.want.UsePathStyle, got.UsePathStyle, "UsePathStyle mismatch")
		})
	}
}

func TestGetFullKey(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		key    string
		want   string
	}{
		{
			name:   "no prefix",
			prefix: "",
			key:    "cache.tar.gz",
			want:   "cache.tar.gz",
		},
		{
			name:   "with prefix",
			prefix: "artifacts",
			key:    "cache.tar.gz",
			want:   "artifacts/cache.tar.gz",
		},
		{
			name:   "with nested prefix",
			prefix: "artifacts/builds",
			key:    "cache.tar.gz",
			want:   "artifacts/builds/cache.tar.gz",
		},
		{
			name:   "key with leading slash",
			prefix: "artifacts",
			key:    "/cache.tar.gz",
			want:   "artifacts/cache.tar.gz",
		},
		{
			name:   "key with path",
			prefix: "artifacts",
			key:    "project/cache.tar.gz",
			want:   "artifacts/project/cache.tar.gz",
		},
		{
			name:   "empty prefix and key",
			prefix: "",
			key:    "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blob := &S3Blob{
				prefix: tt.prefix,
			}
			got := blob.getFullKey(tt.key)
			assert.Equal(t, tt.want, got)
		})
	}
}
