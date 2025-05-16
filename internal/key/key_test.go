package key

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTemplate(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		expected    string
		expectedErr bool
		setup       func() error
		env         map[string]string
		cleanup     func()
	}{
		{
			name:     "simple string",
			key:      "mykey",
			expected: "mykey",
		},
		{
			name:     "string with whitespace",
			key:      "  mykey  ",
			expected: "mykey",
		},
		{
			name:     "invalid template",
			key:      "{{.InvalidField}}",
			expected: "",
		},
		{
			name:     "checksum function with non-existent file",
			key:      `{{checksum "non-existent-file"}}`,
			expected: "",
		},
		{
			name: "checksum function with file",
			key:  `{{checksum "testfile"}}`,
			setup: func() error {
				return os.WriteFile("testfile", []byte("test content"), 0644)
			},
			cleanup: func() {
				_ = os.Remove("testfile")
			},
			expected: "4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660", // This is the expected SHA256 hash
		},
		{
			name: "checksum function with directory",
			key:  `{{checksum "testdir"}}`,
			setup: func() error {
				if err := os.Mkdir("testdir", 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join("testdir", "testfile"), []byte("test content"), 0644)
			},
			cleanup: func() {
				_ = os.RemoveAll("testdir")
			},
			expected: "4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660", // This would be the actual expected hash
		},
		{
			name: "checksum function with directory and BUILDKITE_BRANCH env var",
			key:  `node-{{ env "BUILDKITE_BRANCH" }}-{{ checksum "testdir"}}`,
			setup: func() error {
				if err := os.Mkdir("testdir", 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join("testdir", "testfile"), []byte("test content"), 0644)
			},
			cleanup: func() {
				_ = os.RemoveAll("testdir")
			},
			env: map[string]string{
				"BUILDKITE_BRANCH": "main",
			},
			expected: "node-main-4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660", // This would be the actual expected hash
		},
		{
			name: "checksum function with directory and BUILDKITE_BRANCH env var and runner.os",
			key:  `node-{{ env "BUILDKITE_BRANCH" }}-{{ runner.os }}-{{ runner.arch }}-{{ checksum "testdir"}}`,
			setup: func() error {
				if err := os.Mkdir("testdir", 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join("testdir", "testfile"), []byte("test content"), 0644)
			},
			cleanup: func() {
				_ = os.RemoveAll("testdir")
			},
			env: map[string]string{
				"BUILDKITE_BRANCH": "main",
			},
			expected: fmt.Sprintf("node-main-%s-%s-4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660", runtime.GOOS, runtime.GOARCH), // This would be the actual expected hash
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := require.New(t)

			// Set up environment variables if provided
			if tt.env != nil {
				for key, value := range tt.env {
					t.Setenv(key, value)
				}
			}

			// change working directory to a temp dir
			tmpDir, err := os.MkdirTemp("", "zstash-test")
			assert.NoError(err)
			defer func() {
				_ = os.RemoveAll(tmpDir)
			}()
			err = os.Chdir(tmpDir)
			assert.NoError(err)

			// Set up the test environment
			if tt.setup != nil {
				err := tt.setup()
				assert.NoError(err)
			}

			if tt.cleanup != nil {
				defer tt.cleanup()
			}

			got, err := Template(tt.key)
			assert.NoError(err)
			assert.Equal(tt.expected, got)
		})
	}
}
