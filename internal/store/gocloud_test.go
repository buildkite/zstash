package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGocloudBlob_LocalFile(t *testing.T) {
	// Create a temporary directory for the blob storage
	tmpDir, err := os.MkdirTemp("", "gocloud-blob-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create another temp directory for test files
	testDir, err := os.MkdirTemp("", "gocloud-test-files")
	require.NoError(t, err)
	defer os.RemoveAll(testDir)

	// Create a test file
	testFile := filepath.Join(testDir, "test.txt")
	testContent := "Hello, gocloud.dev!"
	err = os.WriteFile(testFile, []byte(testContent), 0600)
	require.NoError(t, err)

	ctx := context.Background()

	// Create a file-based blob store for testing
	blobURL := "file://" + tmpDir
	blob, err := NewGocloudBlob(ctx, blobURL, "test-prefix")
	require.NoError(t, err)
	defer blob.Close()

	// Test upload
	key := "my-test-file.txt"
	transferInfo, err := blob.Upload(ctx, testFile, key, time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	assert.Greater(t, transferInfo.BytesTransferred, int64(0))
	assert.Greater(t, transferInfo.TransferSpeed, 0.0)

	// Verify file exists in blob storage
	expectedPath := filepath.Join(tmpDir, "test-prefix", key)
	_, err = os.Stat(expectedPath)
	require.NoError(t, err)

	// Test download
	downloadFile := filepath.Join(testDir, "downloaded.txt")
	transferInfo, err = blob.Download(ctx, key, downloadFile)
	require.NoError(t, err)
	assert.Greater(t, transferInfo.BytesTransferred, int64(0))

	// Verify downloaded content
	downloadedContent, err := os.ReadFile(downloadFile)
	require.NoError(t, err)
	assert.Equal(t, testContent, string(downloadedContent))
}

func TestGocloudBlob_Interface(t *testing.T) {
	// This test ensures that GocloudBlob properly implements the Blob interface
	var _ Blob = (*GocloudBlob)(nil)
}
