package key

import (
	"crypto/sha256"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

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

// resolveFiles returns a sorted list of file paths that match the given patterns.
// Patterns starting with "**/)" enable recursive search for the basename.
// Other patterns are handled as specific paths or non-recursive basename matches.
func resolveFiles(patterns []string) ([]string, error) {
	var files []string

	// Parse patterns to determine which need recursive vs non-recursive handling
	recursivePatterns := []string{}
	specificPaths := []string{}
	basenamePatterns := []string{}

	for _, pattern := range patterns {
		switch {
		case strings.HasPrefix(pattern, "**/"):
			// Recursive pattern - extract basename after "*/"
			basename := strings.TrimPrefix(pattern, "**/")
			recursivePatterns = append(recursivePatterns, basename)
			log.Debug().Str("pattern", pattern).Str("basename", basename).Msg("recursive pattern detected")
		case filepath.Dir(pattern) != ".":
			// Specific relative path
			specificPaths = append(specificPaths, pattern)
		default:
			// Basename pattern (non-recursive)
			basenamePatterns = append(basenamePatterns, pattern)
		}
	}

	// Handle specific relative paths first (these work regardless of recursive setting)
	for _, pattern := range specificPaths {
		cleanPath := filepath.Clean(pattern)
		info, err := os.Stat(cleanPath)
		if err == nil {
			if !info.IsDir() {
				// Check if it's in ignore list
				ignored := false
				for _, ignore := range ignoreFiles {
					if strings.HasSuffix(cleanPath, ignore) {
						ignored = true
						break
					}
				}
				if !ignored {
					files = append(files, cleanPath)
					log.Debug().Str("path", cleanPath).Msg("specific path resolved")
				}
			}
		}
	}

	// Build maps for the different pattern types
	recursiveMap := make(map[string]struct{}, len(recursivePatterns))
	for _, pattern := range recursivePatterns {
		recursiveMap[pattern] = struct{}{}
	}

	basenameMap := make(map[string]struct{}, len(basenamePatterns))
	for _, pattern := range basenamePatterns {
		basenameMap[pattern] = struct{}{}
	}

	// Walk to find basename matches (both recursive and non-recursive)
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			log.Error().Err(walkErr).Str("path", path).Msg("error walking path")
			return walkErr
		}

		log.Debug().Str("path", path).Msg("walking path")

		// Apply ignore list to both files and directories
		for _, ignore := range ignoreFiles {
			if strings.HasSuffix(path, ignore) {
				if d.IsDir() {
					log.Debug().Str("path", path).Str("ignore", ignore).Msg("ignoring directory")
					return filepath.SkipDir
				}
				log.Debug().Str("path", path).Str("ignore", ignore).Msg("ignoring file")
				return nil
			}
		}

		// Skip subdirectories if we only have non-recursive patterns
		if d.IsDir() && path != "." && len(recursiveMap) == 0 && len(basenameMap) > 0 {
			log.Debug().Str("path", path).Msg("skipping subdirectory (non-recursive patterns only)")
			return filepath.SkipDir
		}

		// Check for matches (only for files)
		if !d.IsDir() {
			basename := filepath.Base(path)

			// Check recursive patterns
			if _, isRecursive := recursiveMap[basename]; isRecursive {
				files = append(files, path)
				log.Debug().Str("path", path).Str("basename", basename).Msg("recursive pattern matched")
			}

			// Check non-recursive patterns (only in current directory)
			if path == basename || path == "./"+basename {
				if _, isBasename := basenameMap[basename]; isBasename {
					files = append(files, path)
					log.Debug().Str("path", path).Str("basename", basename).Msg("non-recursive pattern matched")
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort for deterministic output
	sort.Strings(files)
	log.Debug().Int("count", len(files)).Msg("files resolved")

	return files, nil
}

func checksum(data []byte) string {
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash[:])
}
