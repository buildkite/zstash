package commands

import (
	"fmt"
	"strings"

	"github.com/buildkite/zstash/internal/api"
	"github.com/buildkite/zstash/internal/key"
	"github.com/rs/zerolog/log"
)

type Globals struct {
	Debug   bool
	Version string
	Client  api.Client
}

func checkPath(path string) ([]string, error) {
	paths := strings.Fields(path)
	if len(paths) == 0 {
		return nil, fmt.Errorf("no paths provided")
	}

	return paths, nil
}

func restoreKeys(id, restoreKeyList string) ([]string, error) {
	restoreKeyTemplates := strings.FieldsFunc(restoreKeyList, func(c rune) bool {
		return c == '\n' || c == '\r'
	})

	restoreKeys := make([]string, len(restoreKeyTemplates))

	log.Info().Str("id", id).Strs("restore_keys", restoreKeyTemplates).Msg("templating restore keys")

	for n, restoreKeyTemplate := range restoreKeyTemplates {

		// trim quotes and whitespace
		restoreKeyTemplate = strings.Trim(restoreKeyTemplate, "\"' \t")

		log.Info().Str("restore_key_template", restoreKeyTemplate).Msg("templating restore key")

		restoreKey, err := key.Template(id, restoreKeyTemplate)
		if err != nil {
			return nil, fmt.Errorf("failed to template restore key: %w", err)
		}

		log.Debug().Str("restore_key", restoreKey).Msg("templated restore key")

		restoreKeys[n] = restoreKey
	}

	return restoreKeys, nil
}
