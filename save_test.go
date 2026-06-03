package zstash

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterExistingPaths(t *testing.T) {
	t.Run("returns all paths when all exist", func(t *testing.T) {
		dir := t.TempDir()
		file1 := filepath.Join(dir, "file1.txt")
		file2 := filepath.Join(dir, "file2.txt")
		require.NoError(t, os.WriteFile(file1, []byte("test1"), 0o644))
		require.NoError(t, os.WriteFile(file2, []byte("test2"), 0o644))

		validPaths, warnings, err := filterExistingPaths([]string{file1, file2})

		require.NoError(t, err)
		assert.Equal(t, []string{file1, file2}, validPaths)
		assert.Empty(t, warnings)
	})

	t.Run("returns warning for missing path", func(t *testing.T) {
		dir := t.TempDir()
		existingFile := filepath.Join(dir, "exists.txt")
		missingFile := filepath.Join(dir, "missing.txt")
		require.NoError(t, os.WriteFile(existingFile, []byte("test"), 0o644))

		validPaths, warnings, err := filterExistingPaths([]string{existingFile, missingFile})

		require.NoError(t, err)
		assert.Equal(t, []string{existingFile}, validPaths)
		assert.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "path does not exist")
		assert.Contains(t, warnings[0], missingFile)
	})

	t.Run("returns error when all paths missing", func(t *testing.T) {
		validPaths, warnings, err := filterExistingPaths([]string{"/nonexistent/path1", "/nonexistent/path2"})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no valid paths found")
		assert.Nil(t, validPaths)
		assert.Len(t, warnings, 2)
	})

	t.Run("returns error for empty paths", func(t *testing.T) {
		validPaths, warnings, err := filterExistingPaths([]string{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no paths provided")
		assert.Nil(t, validPaths)
		assert.Nil(t, warnings)
	})

	t.Run("handles directories", func(t *testing.T) {
		dir := t.TempDir()
		subdir := filepath.Join(dir, "subdir")
		require.NoError(t, os.MkdirAll(subdir, 0o755))

		validPaths, warnings, err := filterExistingPaths([]string{subdir})

		require.NoError(t, err)
		assert.Equal(t, []string{subdir}, validPaths)
		assert.Empty(t, warnings)
	})

	t.Run("handles tilde expansion", func(t *testing.T) {
		homeDir, err := os.UserHomeDir()
		require.NoError(t, err)

		// Create a temp file in home directory for this test
		tempFile := filepath.Join(homeDir, ".zstash_test_temp")
		require.NoError(t, os.WriteFile(tempFile, []byte("test"), 0o644))
		defer os.Remove(tempFile)

		validPaths, warnings, err := filterExistingPaths([]string{"~/.zstash_test_temp"})

		require.NoError(t, err)
		assert.Equal(t, []string{"~/.zstash_test_temp"}, validPaths)
		assert.Empty(t, warnings)
	})

	t.Run("warns for missing tilde path", func(t *testing.T) {
		validPaths, warnings, err := filterExistingPaths([]string{"~/.nonexistent_zstash_test_path", "/tmp"})

		require.NoError(t, err)
		assert.Equal(t, []string{"/tmp"}, validPaths)
		assert.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "~/.nonexistent_zstash_test_path")
	})
}
