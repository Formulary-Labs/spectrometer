package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// moduleRoot walks up to the nearest go.mod.
func moduleRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// buildBinary compiles the spectrometer binary into a temp dir and returns its
// absolute path. It runs `go build` with the module root as the working dir
// so the package path resolves correctly.
func buildBinary(t *testing.T) string {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "spectrometer.exe")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/spectrometer")
	cmd.Dir = moduleRoot()
	outB, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v: %s", err, outB)
	}
	return out
}

// runBinary runs the binary with a working directory and returns the exit
// code and combined output. A non-nil error means the process could not be
// launched at all (not the exit code); exit codes are returned as a value.
func runBinary(bin, workdir string, flags ...string) (int, string, error) {
	args := append([]string{bin}, flags...)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = workdir
	b, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(b), nil
	}
	// exec.ExitError means the process ran but exited non-zero: that's an
	// exit code, not a launch failure.
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(b), nil
	}
	// Anything else is a real launch failure.
	return -1, string(b), err
}

// nativePath normalizes a path to forward slashes (Windows-safe).
func nativePath(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

func TestCLI_Attention(t *testing.T) {
	bin := buildBinary(t)
	pkgRoot := moduleRoot()
	programFile := filepath.Join(pkgRoot, "data/sample/program.json")
	out := filepath.Join(t.TempDir(), "out.json")

	ec, _, err := runBinary(bin, pkgRoot,
		"--program", nativePath(programFile),
		"--now", "2026-09-25T12:00:00Z",
		"--output", out,
	)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	// sample program has an overdue + at-risk + blocked control => attention.
	if ec != 1 {
		t.Fatalf("expected exit 1 (attention), got %d", ec)
	}
	b, _ := os.ReadFile(out)
	var r map[string]interface{}
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("parse report: %v", err)
	}
	if v, ok := r["verdict"].(string); !ok || v != "attention" {
		t.Errorf("verdict=%v; want attention", r["verdict"])
	}
}

func TestCLI_ToolError(t *testing.T) {
	bin := buildBinary(t)
	ec, _, _ := runBinary(bin, moduleRoot(),
		"--program", "/tmp/definitely-does-not-exist.json",
		"--now", "2026-09-25T12:00:00Z",
	)
	if ec != 2 {
		t.Fatalf("tool error: expected exit 2, got %d", ec)
	}
}