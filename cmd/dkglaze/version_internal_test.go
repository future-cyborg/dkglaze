package main

import "testing"

// These tests mutate the package-level version variable, so they must not run
// in parallel.
func TestResolveVersion(t *testing.T) {
	orig := version

	t.Cleanup(func() { version = orig })

	t.Run("injected version is returned as-is", func(t *testing.T) {
		version = "v9.9.9"

		if got := resolveVersion(); got != "v9.9.9" {
			t.Errorf("resolveVersion() = %q, want v9.9.9", got)
		}
	})

	t.Run("dev falls back to build info, which is (devel) under go test", func(t *testing.T) {
		// debug.ReadBuildInfo reports "(devel)" for the main module in test
		// binaries, which resolveVersion treats as "no module version".
		version = devVersion

		if got := resolveVersion(); got != devVersion {
			t.Errorf("resolveVersion() = %q, want %s", got, devVersion)
		}
	})
}
