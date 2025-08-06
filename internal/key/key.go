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

func Template(id, key string, recursive bool) (string, error) {
	tpl := template.New("key").Option("missingkey=zero").Funcs(template.FuncMap{
		"id":       getID(id),
		"checksum": checksumPaths(recursive),
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

func checksumPaths(recursive bool) func(files ...string) string {
	return func(patterns ...string) string {
		log.Info().Strs("files", patterns).Msg("checksumPaths")

		if len(patterns) == 0 {
			return ""
		}

		// Resolve all patterns to actual file paths
		files, err := resolveFiles(patterns, recursive)
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
// Directories are expanded to include all their files. The ignore list and recursive
// settings are consistently applied throughout the resolution process.
func resolveFiles(patterns []string, recursive bool) ([]string, error) {
	var files []string
	
	// First handle specific relative paths (contain directory separators)
	// These should work even in non-recursive mode
	remainingPatterns := []string{}
	for _, pattern := range patterns {
		if filepath.Dir(pattern) != "." {
			// Specific relative path - check if it exists directly
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
				// If it's a directory, we'll handle it in the walk below
			}
		} else {
			// Basename pattern - handle in walk
			remainingPatterns = append(remainingPatterns, pattern)
		}
	}

	// Build a map of remaining patterns for walk
	wantedPatterns := make(map[string]struct{}, len(remainingPatterns))
	for _, pattern := range remainingPatterns {
		wantedPatterns[pattern] = struct{}{}
	}

	// Walk to find basename matches and handle directories
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

		// Skip subdirectories if not recursive
		if !recursive && d.IsDir() && path != "." {
			log.Debug().Str("path", path).Msg("skipping subdirectory (non-recursive)")
			return filepath.SkipDir
		}

		// Check for basename matches (only for files)
		if !d.IsDir() {
			if _, isWanted := wantedPatterns[filepath.Base(path)]; isWanted {
				files = append(files, path)
				log.Debug().Str("path", path).Msg("file basename matched")
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
