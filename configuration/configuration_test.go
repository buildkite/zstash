package configuration

import (
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/buildkite/zstash/cache"
	"github.com/stretchr/testify/require"
)

func TestLoadTemplateDefaults(t *testing.T) {
	t.Run("with no template", func(t *testing.T) {
		tests := []struct {
			name     string
			cache    cache.Cache
			expected cache.Cache
		}{
			{
				name: "with no template",
				cache: cache.Cache{
					ID:           "my_ruby",
					Template:     "",
					Key:          "my-key-overriden",
					FallbackKeys: []string{},
					Paths:        []string{"vendor/bundle"},
				},
				expected: cache.Cache{
					ID:           "my_ruby",
					Template:     "",
					Registry:     "",
					Key:          "my-key-overriden",
					FallbackKeys: []string{},
					Paths:        []string{"vendor/bundle"},
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				assert := require.New(t)

				// Call configuration.ExpandCacheConfiguration to load the template defaults
				got, err := ExpandCacheConfiguration([]cache.Cache{tt.cache})
				assert.NoError(err)
				assert.Equal(tt.expected, got[0], "ExpandCacheConfiguration() should return expected result")
			})
		}
	})

	t.Run("with template (overridden key)", func(t *testing.T) {
		tests := []struct {
			name     string
			cache    cache.Cache
			expected cache.Cache
		}{
			{
				name: "with ruby template",
				cache: cache.Cache{
					ID:       "my_ruby",
					Template: "ruby",
					Key:      "my-key-overriden",
				},
				expected: cache.Cache{
					ID:       "my_ruby",
					Template: "",
					Registry: "",
					Key:      "my-key-overriden",
					FallbackKeys: []string{
						fmt.Sprintf("my_ruby-%s-%s-", runtime.GOOS, runtime.GOARCH),
						"my_ruby-",
					},
					Paths: []string{"vendor/bundle"},
				},
			},
			{
				name: "with node-yarn template",
				cache: cache.Cache{
					ID:       "my_node_yarn",
					Template: "node-yarn",
					Key:      "my-key-overriden",
					Paths:    []string{"node_modules"},
				},
				expected: cache.Cache{
					ID:       "my_node_yarn",
					Template: "",
					Registry: "",
					Key:      "my-key-overriden",
					FallbackKeys: []string{
						fmt.Sprintf("my_node_yarn-%s-%s-", runtime.GOOS, runtime.GOARCH),
						"my_node_yarn-",
					},
					Paths: []string{"node_modules"},
				},
			},
			{
				name: "with node-npm template",
				cache: cache.Cache{
					ID:       "my_node_npm",
					Template: "node-npm",
					Key:      "my-key-overriden",
					Paths:    []string{"node_modules"},
				},
				expected: cache.Cache{
					ID:       "my_node_npm",
					Template: "",
					Registry: "",
					Key:      "my-key-overriden",
					FallbackKeys: []string{
						fmt.Sprintf("my_node_npm-%s-%s-", runtime.GOOS, runtime.GOARCH),
						"my_node_npm-",
					},
					Paths: []string{"node_modules"},
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				assert := require.New(t)

				// Call configuration.ExpandCacheConfiguration to load the template defaults
				got, err := ExpandCacheConfiguration([]cache.Cache{tt.cache})
				assert.NoError(err)
				assert.Equal(tt.expected, got[0], "ExpandCacheConfiguration() should return expected result")
			})
		}
	})

	t.Run("with template (no overriden key)", func(t *testing.T) {
		tests := []struct {
			name     string
			cache    cache.Cache
			setup    func() error
			cleanup  func()
			expected cache.Cache
		}{
			{
				name: "with ruby template",
				setup: func() error {
					return os.WriteFile("Gemfile.lock", []byte("test content"), 0600)
				},
				cleanup: func() {
					_ = os.Remove("Gemfile.lock")
				},
				cache: cache.Cache{
					ID:       "my_ruby",
					Template: "ruby",
				},
				expected: cache.Cache{
					ID:       "my_ruby",
					Template: "",
					Registry: "",
					Key:      fmt.Sprintf("my_ruby-%s-%s-4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660", runtime.GOOS, runtime.GOARCH),
					FallbackKeys: []string{
						fmt.Sprintf("my_ruby-%s-%s-", runtime.GOOS, runtime.GOARCH),
						"my_ruby-",
					},
					Paths: []string{"vendor/bundle"},
				},
			},
			{
				name: "with node-yarn template",
				setup: func() error {
					return os.WriteFile("yarn.lock", []byte("test content"), 0600)
				},
				cleanup: func() {
					_ = os.Remove("yarn.lock")
				},
				cache: cache.Cache{
					ID:       "my_node_yarn",
					Template: "node-yarn",
					Paths:    []string{"node_modules"},
				},
				expected: cache.Cache{
					ID:       "my_node_yarn",
					Template: "",
					Registry: "",
					Key:      fmt.Sprintf("my_node_yarn-%s-%s-4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660", runtime.GOOS, runtime.GOARCH),
					FallbackKeys: []string{
						fmt.Sprintf("my_node_yarn-%s-%s-", runtime.GOOS, runtime.GOARCH),
						"my_node_yarn-",
					},
					Paths: []string{"node_modules"},
				},
			},
			{
				name: "with node-npm template",
				setup: func() error {
					return os.WriteFile("package-lock.json", []byte("test content"), 0600)
				},
				cleanup: func() {
					_ = os.Remove("package-lock.json")
				},
				cache: cache.Cache{
					ID:       "my_node_npm",
					Template: "node-npm",
					Paths:    []string{"node_modules"},
				},
				expected: cache.Cache{
					ID:       "my_node_npm",
					Template: "",
					Registry: "",
					Key:      fmt.Sprintf("my_node_npm-%s-%s-4b9054a7a40e53c2e310fcd6f696c46c6a40dcdfa5b849785a456756ec512660", runtime.GOOS, runtime.GOARCH),
					FallbackKeys: []string{
						fmt.Sprintf("my_node_npm-%s-%s-", runtime.GOOS, runtime.GOARCH),
						"my_node_npm-",
					},
					Paths: []string{"node_modules"},
				},
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

				// Call configuration.ExpandCacheConfiguration to load the template defaults
				got, err := ExpandCacheConfiguration([]cache.Cache{tt.cache})
				assert.NoError(err)
				assert.Equal(tt.expected, got[0], "ExpandCacheConfiguration() should return expected result")
			})
		}
	})
}
