package commands

import (
	"context"
	"fmt"

	"github.com/buildkite/zstash/internal/key"
)

type KeyTestCmd struct {
	ID                 string `arg:"" help:"ID of the cache entry to test." required:"true"`
	Key                string `arg:"" help:"The key to test." required:"true"`
	RecursiveChecksums bool   `flag:"recursive-checksums" help:"Recursively search for matches when generating cache keys."`
}

func (c *KeyTestCmd) Run(ctx context.Context) error {
	key, err := key.Template(c.ID, c.Key, c.RecursiveChecksums)
	if err != nil {
		return fmt.Errorf("failed to template key: %w", err)
	}

	fmt.Println("Templated key:", key)

	// Here you can add more logic to test the key, such as checking its validity
	// or performing any other operations you need.

	return nil
}
