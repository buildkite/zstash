package key

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"html/template"
	"os"
	"runtime"
)

func Resolve(key string, paths []string) (string, error) {
	t := template.Must(template.New("key_template").Funcs(template.FuncMap{
		"shasum": shasum,
		"env":    env,
		"os":     getOS,
		"arch":   getArch,
		"paths":  pathsChecksum(paths),
	}).Parse(key))

	var buf bytes.Buffer

	err := t.Execute(&buf, nil)
	if err != nil {
		return "", fmt.Errorf("failed to execute key template: %w", err)
	}

	return buf.String(), nil
}

func env(key string) (string, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return "", fmt.Errorf("environment variable %s not found", key)
	}

	return value, nil
}

func shasum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}

	defer f.Close()

	sha1sum := sha1.New()
	if _, err := f.WriteTo(sha1sum); err != nil {
		return "", fmt.Errorf("failed to write to sha1: %w", err)
	}

	return fmt.Sprintf("%x", sha1sum.Sum(nil)), nil
}

func getOS() string {
	return runtime.GOOS
}

func getArch() string {
	return runtime.GOARCH
}

func pathsChecksum(paths []string) func() string {
	return func() string {
		sha1sum := sha1.New()

		for _, path := range paths {
			sha1sum.Write([]byte(path))
		}

		return fmt.Sprintf("%x", sha1sum.Sum(nil))
	}
}
