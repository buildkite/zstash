package console

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

func TestPrinter(t *testing.T) {
	assert := require.New(t)

	buf := new(bytes.Buffer)
	printer := NewPrinter(buf)

	// Test Info
	printer.Info("ℹ️", "This is an info message: %s", "test")
	assert.Contains(buf.String(), "ℹ️ This is an info message: test")

	// Test Success
	buf.Reset()
	printer.Success("✅", "This is a success message: %s", "test")
	assert.Contains(buf.String(), "✅ This is a success message: test")

	// Test Warn
	buf.Reset()
	printer.Warn("⚠️", "This is a warning message: %s", "test")
	assert.Contains(buf.String(), "⚠️ This is a warning message: test")

	// Test Error
	buf.Reset()
	printer.Error("❌", "This is an error message: %s", "test")
	assert.Contains(buf.String(), "❌ This is an error message: test")
}

// TestPrinterWithColors tests color output by forcing color mode
func TestPrinterWithColors(t *testing.T) {
	assert := require.New(t)

	buf := new(bytes.Buffer)
	printer := NewPrinter(buf)

	// Force color output by setting renderer color profile
	printer.renderer.SetColorProfile(termenv.TrueColor)

	// Test that colored output contains ANSI escape codes
	printer.Success("✅", "Success message")
	output := buf.String()

	// Check for ANSI escape sequences (color codes)
	assert.Contains(output, "\x1b[", "Expected ANSI escape sequence for colors")
	assert.Contains(output, "✅ Success message")

	// Test different colors
	buf.Reset()
	printer.Warn("⚠️", "Warning message")
	assert.Contains(buf.String(), "\x1b[", "Expected ANSI escape sequence for warning")

	buf.Reset()
	printer.Error("❌", "Error message")
	assert.Contains(buf.String(), "\x1b[", "Expected ANSI escape sequence for error")
}

// TestPrinterColorOutput demonstrates color output for manual testing
func TestPrinterColorOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping color output test in short mode")
	}

	// This test is for manual verification - run with: go test -v -run TestPrinterColorOutput
	printer := NewPrinter(os.Stderr)

	// Force color output for testing
	printer.renderer.SetColorProfile(termenv.TrueColor)

	t.Log("\n=== Manual Color Output Test ===")
	t.Log("The following should show colored output:")

	printer.Info("ℹ️", "This is an info message (blue)")
	printer.Success("✅", "This is a success message (green)")
	printer.Warn("⚠️", "This is a warning message (amber)")
	printer.Error("❌", "This is an error message (red)")

	// Also test with a buffer to show raw output
	buf := new(bytes.Buffer)
	bufPrinter := NewPrinter(buf)
	bufPrinter.renderer.SetColorProfile(termenv.TrueColor)

	bufPrinter.Success("✅", "Buffer test")
	t.Logf("Raw output with escape codes: %q", buf.String())

	// Test what happens without forced colors
	buf.Reset()
	bufPrinter2 := NewPrinter(buf)
	bufPrinter2.Success("✅", "Buffer test no colors")
	t.Logf("Raw output without colors: %q", buf.String())
}

// TestWithEmoji tests the emoji helper function
func TestWithEmoji(t *testing.T) {
	assert := require.New(t)

	assert.Equal("✅ ", withEmoji("✅"))
	assert.Equal("", withEmoji(""))
	assert.Equal("test ", withEmoji("test"))
}

// TestIndentation tests that all methods use proper indentation
func TestIndentation(t *testing.T) {
	assert := require.New(t)

	buf := new(bytes.Buffer)
	printer := NewPrinter(buf)

	printer.Info("", "test")
	assert.True(strings.HasPrefix(buf.String(), "  "), "Should start with indent")

	buf.Reset()
	printer.Success("", "test")
	assert.True(strings.HasPrefix(buf.String(), "  "), "Should start with indent")

	buf.Reset()
	printer.Warn("", "test")
	assert.True(strings.HasPrefix(buf.String(), "  "), "Should start with indent")

	buf.Reset()
	printer.Error("", "test")
	assert.True(strings.HasPrefix(buf.String(), "  "), "Should start with indent")
}
