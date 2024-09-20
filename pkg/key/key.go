package key

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"html/template"
	"os"
)

func Resolve(key string) (string, error) {

	t := template.Must(template.New("test").Funcs(template.FuncMap{
		"shasum": shasum,
		"env":    env,
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

	sha256 := sha256.New()
	if _, err := f.WriteTo(sha256); err != nil {
		return "", fmt.Errorf("failed to write to sha256: %w", err)
	}

	return fmt.Sprintf("%x", sha256.Sum(nil)), nil
}
