package console

import (
	"os"
	"testing"
)

func TestPinter(t *testing.T) {
	printer := NewPrinter(os.Stdout)

	// Test Info
	n, err := printer.Info("ℹ️", "This is an info message: %s", "test")
	if err != nil {
		t.Errorf("Info failed: %v", err)
	}
	if n <= 0 {
		t.Error("Info did not write any bytes")
	}

	// Test Warn
	n, err = printer.Warn("⚠️", "This is a warning message: %s", "test")
	if err != nil {
		t.Errorf("Warn failed: %v", err)
	}
	if n <= 0 {
		t.Error("Warn did not write any bytes")
	}
}
