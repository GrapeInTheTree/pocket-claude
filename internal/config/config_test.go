package config

import (
	"os"
	"testing"
)

func TestEnvOrDefault(t *testing.T) {
	// Unset → default
	os.Unsetenv("TEST_ENV_OR_DEFAULT")
	if got := envOrDefault("TEST_ENV_OR_DEFAULT", "fallback"); got != "fallback" {
		t.Errorf("Expected 'fallback', got %q", got)
	}

	// Set → value
	os.Setenv("TEST_ENV_OR_DEFAULT", "custom")
	defer os.Unsetenv("TEST_ENV_OR_DEFAULT")
	if got := envOrDefault("TEST_ENV_OR_DEFAULT", "fallback"); got != "custom" {
		t.Errorf("Expected 'custom', got %q", got)
	}

	// Empty string → default
	os.Setenv("TEST_ENV_OR_DEFAULT", "")
	if got := envOrDefault("TEST_ENV_OR_DEFAULT", "fallback"); got != "fallback" {
		t.Errorf("Expected 'fallback' for empty value, got %q", got)
	}
}

func TestEnvIntOrDefault(t *testing.T) {
	// Unset → default
	os.Unsetenv("TEST_ENV_INT")
	if got := envIntOrDefault("TEST_ENV_INT", 42); got != 42 {
		t.Errorf("Expected 42, got %d", got)
	}

	// Valid int
	os.Setenv("TEST_ENV_INT", "100")
	defer os.Unsetenv("TEST_ENV_INT")
	if got := envIntOrDefault("TEST_ENV_INT", 42); got != 100 {
		t.Errorf("Expected 100, got %d", got)
	}

	// Negative int
	os.Setenv("TEST_ENV_INT", "-5")
	if got := envIntOrDefault("TEST_ENV_INT", 42); got != -5 {
		t.Errorf("Expected -5, got %d", got)
	}

	// Invalid int → default
	os.Setenv("TEST_ENV_INT", "not_a_number")
	if got := envIntOrDefault("TEST_ENV_INT", 42); got != 42 {
		t.Errorf("Expected 42 for invalid int, got %d", got)
	}

	// Empty → default
	os.Setenv("TEST_ENV_INT", "")
	if got := envIntOrDefault("TEST_ENV_INT", 42); got != 42 {
		t.Errorf("Expected 42 for empty value, got %d", got)
	}
}

func TestAcquirePIDFileCreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := tmpDir + "/test.pid"

	if err := AcquirePIDFile(pidPath); err != nil {
		t.Fatalf("AcquirePIDFile: %v", err)
	}

	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Error("PID file is empty")
	}
}
