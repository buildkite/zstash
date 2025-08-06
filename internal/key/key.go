package key

import (
	"crypto/sha256"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/rs/zerolog/log"
)

var ignoreFiles = []string{
	".DS_Store",
	"Thumbs.db",
	".git",
	".hg",
	".svn",
	".bzr",
	".vscode",
	".idea",
	".keep",
}

func Template(id, key string) (string, error) {
	tpl := template.New("key").Option("missingkey=zero").Funcs(template.FuncMap{
		"id":       getID(id),
		"checksum": checksumPaths(),
		"env":      getEnv,
		"agent":    getAgent,
	})
	tpl, err := tpl.Parse(key)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	err = tpl.Execute(&sb, nil)
	if err != nil {
		return "", err
	}
	key = sb.String()

	// remove all leading and trailing whitespace
	key = strings.TrimSpace(key)

	return key, nil
}

func getID(id string) func() string {
	return func() string {
		log.Debug().Str("id", id).Msg("getID")
		if id == "" {
			return ""
		}
		// remove all leading and trailing whitespace
		id = strings.TrimSpace(id)
		return id
	}
}

func getAgent() map[string]string {
	return map[string]string{
		"os":   runtime.GOOS,
		"arch": runtime.GOARCH,
	}
}

func getEnv(key string) string {

	log.Info().Str("key", key).Msg("getEnv")

	// get the env variable
	env := os.Getenv(key)
	if env == "" {
		return ""
	}

	// remove all leading and trailing whitespace
	env = strings.TrimSpace(env)

	return env
}

func checksumPaths() func(files ...string) string {
	return func(patterns ...string) string {
		log.Info().Strs("files", patterns).Msg("checksumPaths")

		if len(patterns) == 0 {
			return ""
		}

		// Resolve all patterns to actual file paths
		files, err := resolveFiles(patterns)
		if err != nil {
			log.Error().Err(err).Msg("error resolving files")
			return ""
		}

		if len(files) == 0 {
			log.Warn().Strs("patterns", patterns).Msg("no files found for patterns")
			return ""
		}

		log.Info().Int("files", len(files)).Msg("resolved files for checksumming")

		// Calculate individual checksums and combine (for backward compatibility)
		var sums []string
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				log.Error().Err(err).Str("file", file).Msg("error reading file")
				return ""
			}
			sums = append(sums, checksum(data))
			log.Debug().Str("file", file).Msg("checksummed file")
		}

		// Combine the sums into a single string and hash (matches original behavior)
		combinedSums := strings.Join(sums, "")
		return checksum([]byte(combinedSums))
	}
}

// resolveFiles returns all files that match any of the supplied glob patterns.
// Uses doublestar for full glob pattern support including **, *, ?, [], {a,b}.
// Maintains backward compatibility with existing patterns while adding standard glob capabilities.
func resolveFiles(patterns []string) ([]string, error) {
	seen := make(map[string]struct{})
	var result []string

	for _, pattern := range patterns {
		log.Debug().Str("pattern", pattern).Msg("processing glob pattern")

		// Use doublestar to find all matches for this pattern
		matches, err := doublestar.Glob(os.DirFS("."), pattern)
		if err != nil {
			log.Error().Err(err).Str("pattern", pattern).Msg("glob pattern failed")
			return nil, err
		}

		for _, match := range matches {
			// Convert to platform-specific path separators
			match = filepath.FromSlash(match)

			// Only include files, not directories
			info, err := os.Stat(match)
			if err != nil || info.IsDir() {
				continue
			}

			// Apply ignore list
			ignored := false
			for _, ignore := range ignoreFiles {
				if strings.HasSuffix(match, ignore) {
					ignored = true
					log.Debug().Str("path", match).Str("ignore", ignore).Msg("ignoring file")
					break
				}
			}

			if !ignored {
				// Deduplicate
				if _, exists := seen[match]; !exists {
					seen[match] = struct{}{}
					result = append(result, match)
					log.Debug().Str("path", match).Str("pattern", pattern).Msg("file matched")
				}
			}
		}
	}

	// Sort for deterministic output
	sort.Strings(result)
	log.Debug().Int("count", len(result)).Msg("files resolved")

	return result, nil
}

func checksum(data []byte) string {
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash[:])
}
