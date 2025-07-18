package key

import (
	"crypto/sha256"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"runtime"
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
	return func(files ...string) string {
		log.Info().Strs("files", files).Msg("checksumPaths")

		if len(files) == 0 {
			return ""
		}

		sums := []string{}

		for _, filename := range files {

			log.Debug().Str("file", filename).Msg("checksumPaths file")

			// recursively find any files or directories which match the file
			matchedFiles, matchedDirs, err := matchFilesAndDirs(filename, recursive)
			if err != nil {
				log.Error().Err(err).Str("file", filename).Msg("error matching files and directories")
				return ""
			}

			log.Info().Int("dirs", len(matchedDirs)).Int("filenames", len(matchedFiles)).Msg("found files and directories")

			if len(matchedFiles) == 0 && len(matchedDirs) == 0 {
				log.Warn().Str("file", filename).Msg("no files or directories found")
				return ""
			}

			for _, file := range matchedFiles {
				// if the file is a regular file, we can read it and get the checksum
				data, err := os.ReadFile(file)
				if err != nil {
					log.Error().Err(err).Str("file", file).Msg("error reading file")
					return ""
				}
				sums = append(sums, checksum(data))
				log.Debug().Str("file", file).Msg("checksum file")
			}

			for _, dir := range matchedDirs {
				// if the file is a directory, we need to walk the directory and get the checksums of all files in the directory
				log.Debug().Str("dir", dir).Msg("walking directory")
				err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						log.Error().Err(err).Str("path", path).Msg("error walking directory")
						return err
					}
					log.Debug().Str("path", path).Msg("walking path in directory")
					if info.IsDir() {
						log.Debug().Str("path", path).Msg("skipping directory")
						return nil
					}
					// read the file and get the checksum
					data, err := os.ReadFile(path)
					if err != nil {
						log.Error().Err(err).Str("file", path).Msg("error reading file in directory")
						return err
					}
					sums = append(sums, checksum(data))
					log.Debug().Str("file", path).Msg("read file in directory")
					return nil
				})
				if err != nil {
					log.Error().Err(err).Str("dir", dir).Msg("error walking directory")
					return ""
				}
			}
		}

		log.Debug().Int("files", len(sums)).Msg("checksums calculated")

		// combine the sums into a single string
		combinedSums := strings.Join(sums, "")

		// use sha256 to get the checksum of the combined hashes
		return checksum([]byte(combinedSums))
	}
}

func matchFilesAndDirs(filename string, recursive bool) ([]string, []string, error) {
	matchedDirs := []string{}
	matchedFiles := []string{}

	// Check if the filename is a specific relative path (contains directory separators)
	if filepath.Dir(filename) != "." {
		// Handle as a specific relative path
		cleanPath := filepath.Clean(filename)
		info, err := os.Stat(cleanPath)
		if err != nil {
			log.Debug().Err(err).Str("path", cleanPath).Msg("specific path not found")
			return matchedFiles, matchedDirs, nil
		}

		if info.IsDir() {
			matchedDirs = append(matchedDirs, cleanPath)
		} else {
			matchedFiles = append(matchedFiles, cleanPath)
		}
		return matchedFiles, matchedDirs, nil
	}

	// Original logic for matching by basename
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Error().Err(err).Str("path", path).Msg("error walking path")
			return err
		}

		log.Debug().Str("path", path).Msg("walking path")

		// skip subdirectories if not recursive
		if !recursive && info.IsDir() && path != "." {
			log.Debug().Str("path", path).Msg("skipping subdirectory (non-recursive)")
			return filepath.SkipDir
		}

		// skip if the file is in the ignore list
		for _, ignore := range ignoreFiles {
			if strings.HasSuffix(path, ignore) {

				if info.IsDir() {
					log.Debug().Str("path", path).Msg("ignoring directory")
					return filepath.SkipDir // Skip the directory if it matches an ignore file
				}

				log.Debug().Str("path", path).Msg("ignoring file")
				return nil // Skip the file if it matches an ignore file
			}
		}

		// does the file match the file?
		if filepath.Base(path) != filename {
			return nil
		}

		if info.IsDir() {
			matchedDirs = append(matchedDirs, path)
			return nil
		}

		matchedFiles = append(matchedFiles, path)

		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	return matchedFiles, matchedDirs, err
}

func checksum(data []byte) string {
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash[:])
}
