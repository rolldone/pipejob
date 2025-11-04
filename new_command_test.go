package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewCommandCreatesFile(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "gen.yaml")
	rc := RunWithArgs([]string{"new", out})
	if rc != 0 {
		t.Fatalf("new command failed: rc=%d", rc)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("expected generated file at %s but stat failed: %v", out, err)
	}
}
