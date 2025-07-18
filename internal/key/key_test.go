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
	t.Run("basic templates", func(t *testing.T) {
		tests := []struct {
			name     string
			key      string
			expected string
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
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got, err := Template("", tt.key, false)
				require.NoError(t, err)
				require.Equal(t, tt.expected, got)
			})
		}
	})

	t.Run("checksum templates", func(t *testing.T) {
		tests := []struct {
			name      string
			id        string
			key       string
			recursive bool
			setup     func() error
			cleanup   func()
			expected  string
		}{
			{
				name:     "non-existent file",
				key:      `{{checksum "non-existent-file"}}`,
				expected: "",
			},
			{
				name: "single file",
				id:   "go",
				key:  `{{ id }}-{{checksum "go.mod"}}`,
				setup: func() error {
					return os.WriteFile("go.mod", []byte("test content"), 0600)
				},
				cleanup: func() {
					_ = os.Remove("go.mod")
				},
				expected: "go-4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660",
			},
			{
				name: "file with os/arch",
				id:   "go",
				key:  `{{ id }}-{{ agent.os }}-{{ agent.arch }}-{{checksum "go.mod"}}`,
				setup: func() error {
					return os.WriteFile("go.mod", []byte("test content"), 0600)
				},
				cleanup: func() {
					_ = os.Remove("go.mod")
				},
				expected: fmt.Sprintf("go-%s-%s-4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660", runtime.GOOS, runtime.GOARCH),
			},
			{
				name:      "file non-recursive (only root)",
				key:       `go-{{checksum "go.mod"}}`,
				recursive: false,
				setup: func() error {
					if err := os.Mkdir("subdir", 0755); err != nil {
						return err
					}
					if err := os.WriteFile(filepath.Join("subdir", "go.mod"), []byte("nested content"), 0600); err != nil {
						return err
					}
					return os.WriteFile("go.mod", []byte("test content"), 0600)
				},
				cleanup: func() {
					_ = os.Remove("go.mod")
					_ = os.RemoveAll("subdir")
				},
				expected: "go-4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660",
			},
			{
				name:      "file recursive (finds all)",
				key:       `go-{{checksum "go.mod"}}`,
				recursive: true,
				setup: func() error {
					if err := os.Mkdir("subdir", 0755); err != nil {
						return err
					}
					if err := os.WriteFile(filepath.Join("subdir", "go.mod"), []byte("nested content"), 0600); err != nil {
						return err
					}
					return os.WriteFile("go.mod", []byte("test content"), 0600)
				},
				cleanup: func() {
					_ = os.Remove("go.mod")
					_ = os.RemoveAll("subdir")
				},
				expected: "go-f2684b75ab846895bcc1d50f4511edeb8fcd86167a8e6e64aeee46afc1576d9c",
			},
			{
				name:      "directory recursive",
				key:       `{{checksum "testfile"}}`,
				recursive: true,
				setup: func() error {
					if err := os.Mkdir("testdir", 0755); err != nil {
						return err
					}
					return os.WriteFile(filepath.Join("testdir", "testfile"), []byte("test content"), 0600)
				},
				cleanup: func() {
					_ = os.RemoveAll("testdir")
				},
				expected: "4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660",
			},
			{
				name:      "directory non-recursive (empty)",
				key:       `{{checksum "testdir"}}`,
				recursive: false,
				setup: func() error {
					if err := os.Mkdir("testdir", 0755); err != nil {
						return err
					}
					return os.WriteFile(filepath.Join("testdir", "testfile"), []byte("test content"), 0600)
				},
				cleanup: func() {
					_ = os.RemoveAll("testdir")
				},
				expected: "",
			},
			{
				name:      "file path non-recursive",
				key:       `{{checksum "testdir/Dockerfile.dev"}}`,
				recursive: false,
				setup: func() error {
					if err := os.Mkdir("testdir", 0755); err != nil {
						return err
					}
					return os.WriteFile(filepath.Join("testdir", "Dockerfile.dev"), []byte("test content"), 0600)
				},
				cleanup: func() {
					_ = os.RemoveAll("testdir")
				},
				expected: "4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				assert := require.New(t)

				// Create temp directory
				tmpDir, err := os.MkdirTemp("", "zstash-test")
				assert.NoError(err)
				defer func() {
					_ = os.RemoveAll(tmpDir)
				}()
				err = os.Chdir(tmpDir)
				assert.NoError(err)

				// Setup test environment
				if tt.setup != nil {
					err := tt.setup()
					assert.NoError(err)
				}

				if tt.cleanup != nil {
					defer tt.cleanup()
				}

				got, err := Template(tt.id, tt.key, tt.recursive)
				assert.NoError(err)
				assert.Equal(tt.expected, got)
			})
		}
	})
}
