package zstash

import (
	"fmt"
	"runtime"

	"github.com/buildkite/zstash/cache"
	"github.com/buildkite/zstash/configuration"
)

// NewCache creates and validates a new cache client.
//
// This function:
//  1. Validates the configuration
//  2. Expands cache template variables using cfg.Env (if provided)
//  3. Validates all cache configurations
//  4. Returns a ready-to-use cache client
//
// Returns an error if configuration is invalid or cache validation fails.
func NewCache(cfg Config) (*Cache, error) {
	// Validate required configuration
	// Note: Client is a struct, so we can't check for nil. It should be created via NewClient.
	// We trust that if passed, it was properly initialized.

	// Set defaults
	if cfg.Format == "" {
		cfg.Format = "zip"
	}

	if cfg.Platform == "" {
		cfg.Platform = fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	}

	var (
		err error
		// Expand cache configurations
		expandedCaches []cache.Cache
	)

	if cfg.Env != nil {
		// If environment is provided, expand cache templates
		expandedCaches, err = configuration.ExpandCacheConfigurationWithEnv(cfg.Caches, cfg.Env)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to expand cache configuration: %w", ErrInvalidConfiguration, err)
		}
	} else {
		// Use OS environment for expansion
		expandedCaches, err = configuration.ExpandCacheConfiguration(cfg.Caches)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to expand cache configuration: %w", ErrInvalidConfiguration, err)
		}
	}

	// Validate all caches
	for _, c := range expandedCaches {
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("%w: cache validation failed for ID %s: %w", ErrInvalidConfiguration, c.ID, err)
		}
	}

	return &Cache{
		client:       cfg.Client,
		bucketURL:    cfg.BucketURL,
		format:       cfg.Format,
		branch:       cfg.Branch,
		pipeline:     cfg.Pipeline,
		organization: cfg.Organization,
		platform:     cfg.Platform,
		caches:       expandedCaches,
		onProgress:   cfg.OnProgress,
	}, nil
}

// callProgress safely calls the progress callback if it exists
func (c *Cache) callProgress(stage string, message string, current int, total int) {
	if c.onProgress != nil {
		// Protect against panics in user-provided callback
		defer func() {
			if r := recover(); r != nil {
				// Log but don't crash - user callbacks shouldn't break the cache client
			}
		}()
		c.onProgress(stage, message, current, total)
	}
}

// findCache finds a cache by ID in the cache client's cache list
func (c *Cache) findCache(id string) (*cache.Cache, error) {
	for i := range c.caches {
		if c.caches[i].ID == id {
			return &c.caches[i], nil
		}
	}
	return nil, ErrCacheNotFound
}
