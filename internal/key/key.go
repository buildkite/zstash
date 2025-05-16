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

func Template(key string) (string, error) {
	tpl := template.New("key").Option("missingkey=zero").Funcs(template.FuncMap{
		"checksum": checksumPaths,
		"env":      getEnv,
		"runner":   getRunner,
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

func getRunner() map[string]string {
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

func checksumPaths(files ...string) string {

	log.Info().Strs("files", files).Msg("checksumPaths")

	// if this is a file then we need to get the checksum of the file
	// if this is a directory then we need to get the checksum of the directory

	if len(files) == 0 {
		return ""
	}

	sums := []string{}

	for _, file := range files {
		// if this is a file then we need to get the checksum of the file
		// if this is a directory then we need to get the checksum of the directory

		fileInfo, err := os.Stat(file)
		if err != nil {
			return ""
		}

		if fileInfo.IsDir() {
			// recursively get the checksum of the directory
			err := filepath.Walk(file, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					return nil
				}

				// read the file and get the checksum

				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}

				sums = append(sums, checksum(data))

				return nil
			})
			if err != nil {
				return ""
			}
		} else {

			data, err := os.ReadFile(file)
			if err != nil {
				return ""
			}

			// get the checksum of the file
			sums = append(sums, checksum(data))
		}
	}

	log.Info().Strs("files", sums).Msg("sums")

	// combine the sums into a single string
	combinedSums := strings.Join(sums, "")

	// use sha256 to get the checksum of the combined hashes
	return checksum([]byte(combinedSums))
}

func checksum(data []byte) string {
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash[:])
}
