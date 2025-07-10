package console

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrinter(t *testing.T) {
	assert := require.New(t)

	buf := new(bytes.Buffer)

	printer := NewPrinter(buf)

	// Test Info
	printer.Info("ℹ️", "This is an info message: %s", "test")

	assert.Contains(buf.String(), "ℹ️ This is an info message: test")

	buf = new(bytes.Buffer)

	printer = NewPrinter(buf)

	// Test Warn
	printer.Warn("⚠️", "This is a warning message: %s", "test")

	assert.Contains(buf.String(), "⚠️ This is a warning message: test")

}
