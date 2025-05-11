package archive

import (
	"context"
	"os"
	"testing"

	"github.com/buildkite/zstash/internal/trace"
	"github.com/stretchr/testify/require"
)

func TestBuildArchive(t *testing.T) {
	assert := require.New(t)

	_, err := trace.NewProvider(context.Background(), "test", "0.0.1")
	assert.NoError(err)

	home, err := os.Getwd()
	assert.NoError(err)

	os.Setenv("HOME", home)

	archiveInfo, err := BuildArchive(context.Background(), []string{"testdata"}, "test")
	assert.NoError(err)
	assert.Equal("3f194172652432099ffc528f81ea6ce1687780287cba1d1a9587f5c26c72aeac", archiveInfo.Sha256sum)
	assert.Equal(int64(1228), archiveInfo.Size)

	homeDir, err := os.UserHomeDir()
	assert.NoError(err)
	assert.Equal(home, homeDir)
}
