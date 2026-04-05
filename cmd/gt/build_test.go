package main

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// TestCrossPlatformBuild verifies that the codebase compiles for supported
// platforms. This fork is Linux-only (non-Linux platform code was removed
// in commit f04b750b), so we only test Linux targets.
func TestCrossPlatformBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cross-platform build test in short mode")
	}

	// Skip if not running on a platform that can cross-compile
	// (need Go toolchain, not just running tests)
	if os.Getenv("CI") == "" && runtime.GOOS != "linux" {
		t.Skip("skipping build test on non-Linux platform")
	}

	platforms := []struct {
		goos   string
		goarch string
		cgo    string
	}{
		{"linux", "amd64", "0"},
		{"linux", "arm64", "0"},
	}

	for _, p := range platforms {
		p := p // capture range variable
		t.Run(p.goos+"_"+p.goarch, func(t *testing.T) {
			t.Parallel()

			cmd := exec.Command("go", "build", "-o", os.DevNull, ".")
			cmd.Dir = "."
			cmd.Env = append(os.Environ(),
				"GOOS="+p.goos,
				"GOARCH="+p.goarch,
				"CGO_ENABLED="+p.cgo,
			)

			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("build failed for %s/%s:\n%s", p.goos, p.goarch, string(output))
			}
		})
	}
}
