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
		id          string
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
			id:   "go",
			key:  `{{ id }}-{{checksum "go.mod"}}`,
			setup: func() error {
				return os.WriteFile("go.mod", []byte("test content"), 0600)
			},
			cleanup: func() {
				_ = os.Remove("testfile")
			},
			expected: "go-4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660", // This is the expected SHA256 hash
		},
		{
			name: "checksum function with file and os/arch",
			id:   "go",
			key:  `{{ id }}-{{ agent.os }}-{{ agent.arch }}-{{checksum "go.mod"}}`,
			setup: func() error {
				return os.WriteFile("go.mod", []byte("test content"), 0600)
			},
			cleanup: func() {
				_ = os.Remove("testfile")
			},
			expected: fmt.Sprintf("go-%s-%s-4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660", runtime.GOOS, runtime.GOARCH), // This is the expected SHA256 hash
		},
		{
			name: "checksum function with file and directory",
			key:  `go-{{checksum "go.mod"}}`,
			setup: func() error {
				if err := os.Mkdir("integration-tests", 0755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join("integration-tests", "go.mod"), []byte("test content"), 0600); err != nil {
					return err
				}
				return os.WriteFile("go.mod", []byte("test content"), 0600)
			},
			cleanup: func() {
				_ = os.Remove("testfile")
				_ = os.RemoveAll("testdir")
			},
			expected: "go-41a16a34ed93bb76eb778de4bb735de7d43ff39ffa1c60027e1616cade39712b", // This is the expected SHA256 hash
		},
		{
			name: "checksum function with directory",
			key:  `{{checksum "testdir"}}`,
			setup: func() error {
				if err := os.Mkdir("testdir", 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join("testdir", "testfile"), []byte("test content"), 0600)
			},
			cleanup: func() {
				_ = os.RemoveAll("testdir")
			},
			expected: "4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660", // This would be the actual expected hash
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

			got, err := Template(tt.id, tt.key)
			assert.NoError(err)
			assert.Equal(tt.expected, got)
		})
	}
}
