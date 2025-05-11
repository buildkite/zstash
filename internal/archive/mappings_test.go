package archive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveHomeDir(t *testing.T) {
	assert := require.New(t)

	homeDir, err := os.UserHomeDir()
	assert.NoError(err)

	tests := []struct {
		name     string
		path     string
		expected string
		wantErr  bool
	}{
		{
			name:     "path with tilde prefix",
			path:     "~/documents/test.txt",
			expected: filepath.Join(homeDir, "documents/test.txt"),
			wantErr:  false,
		},
		{
			name:     "path without tilde",
			path:     "/absolute/path/test.txt",
			expected: "/absolute/path/test.txt",
			wantErr:  false,
		},
		{
			name:     "empty path",
			path:     "",
			expected: "",
			wantErr:  false,
		},
		{
			name:     "tilde only",
			path:     "~",
			expected: "~",
			wantErr:  false,
		},
		{
			name:     "relative path",
			path:     "./test.txt",
			expected: "./test.txt",
			wantErr:  false,
		},
		{
			name:     "path with multiple tildes",
			path:     "~/path/~/test.txt",
			expected: filepath.Join(homeDir, "path/~/test.txt"),
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolveHomeDir(tt.path)
			if tt.wantErr {
				assert.Error(err)
				return
			}
			assert.NoError(err)
			assert.Equal(tt.expected, result)
		})
	}
}
