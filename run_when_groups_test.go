package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWhenGroupsAllAny(t *testing.T) {
	// all -> both must match
	p1 := filepath.Join("examples", "when-groups-all.yaml")
	out := captureStdout(func() {
		rc := RunWithArgs([]string{p1})
		if rc != 0 {
			t.Fatalf("non-zero exit for all case: %d", rc)
		}
	})
	if !strings.Contains(out, "ALL_OK") {
		t.Fatalf("expected ALL_OK in output for all-case, got: %s", out)
	}

	// any -> one of the conditions matches
	p2 := filepath.Join("examples", "when-groups-any.yaml")
	out = captureStdout(func() {
		rc := RunWithArgs([]string{p2})
		if rc != 0 {
			t.Fatalf("non-zero exit for any case: %d", rc)
		}
	})
	if !strings.Contains(out, "ANY_OK") {
		t.Fatalf("expected ANY_OK in output for any-case, got: %s", out)
	}
}
