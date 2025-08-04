package commands

import (
	"testing"

	"github.com/buildkite/zstash/internal/store"
	"github.com/stretchr/testify/require"
)

func TestValidateCacheRegistry(t *testing.T) {
	tests := []struct {
		name     string
		storeVal string
		common   CommonFlags
		wantErr  bool
		errMsg   string
	}{
		{
			name:     "valid s3 store with s3:// bucket URL",
			storeVal: store.LocalS3Store,
			common:   CommonFlags{BucketURL: "s3://my-bucket"},
			wantErr:  false,
		},
		{
			name:     "valid s3 store with file:// bucket URL",
			storeVal: store.LocalS3Store,
			common:   CommonFlags{BucketURL: "file:///tmp/cache"},
			wantErr:  false,
		},
		{
			name:     "valid nsc store with empty config",
			storeVal: store.LocalNscStore,
			common:   CommonFlags{BucketURL: ""},
			wantErr:  false,
		},
		{
			name:     "invalid store type",
			storeVal: "invalid_store",
			common:   CommonFlags{BucketURL: "s3://my-bucket"},
			wantErr:  true,
			errMsg:   "unsupported cache store: invalid_store",
		},
		{
			name:     "s3 store with invalid bucket URL",
			storeVal: store.LocalS3Store,
			common:   CommonFlags{BucketURL: "https://my-bucket"},
			wantErr:  true,
			errMsg:   "bucket URL for S3 store must start with 's3://' or 'file://'",
		},
		{
			name:     "s3 store with http bucket URL",
			storeVal: store.LocalS3Store,
			common:   CommonFlags{BucketURL: "http://my-bucket"},
			wantErr:  true,
			errMsg:   "bucket URL for S3 store must start with 's3://' or 'file://'",
		},
		{
			name:     "s3 store with empty bucket URL",
			storeVal: store.LocalS3Store,
			common:   CommonFlags{BucketURL: ""},
			wantErr:  true,
			errMsg:   "bucket URL for S3 store must start with 's3://' or 'file://'",
		},
		{
			name:     "s3 store with gs:// bucket URL",
			storeVal: store.LocalS3Store,
			common:   CommonFlags{BucketURL: "gs://my-bucket"},
			wantErr:  true,
			errMsg:   "bucket URL for S3 store must start with 's3://' or 'file://'",
		},
		{
			name:     "nsc store with bucket URL should fail",
			storeVal: store.LocalNscStore,
			common:   CommonFlags{BucketURL: "s3://my-bucket"},
			wantErr:  true,
			errMsg:   "NSC store should not have bucket URL set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := require.New(t)
			err := validateCacheRegistry(tt.storeVal, tt.common)
			if tt.wantErr {
				assert.Error(err, "validateCacheRegistry() expected error but got none")
				assert.Contains(err.Error(), tt.errMsg, "error message should contain expected text")
			} else {
				assert.NoError(err, "validateCacheRegistry() should not return error")
			}
		})
	}
}
