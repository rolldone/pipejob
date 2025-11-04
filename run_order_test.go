package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureStdout(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	outC := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		outC <- buf.String()
	}()
	f()
	w.Close()
	os.Stdout = old
	return <-outC
}

func TestRunsOrder(t *testing.T) {
	tmp := t.TempDir()
	yamlPath := filepath.Join(tmp, "job.yaml")
	yaml := `pipeline:
  name: ordered
  runs: [second, first]
  jobs:
    - name: first
      steps:
        - name: s1
          type: command
          command: echo "FIRST"
    - name: second
      steps:
        - name: s2
          type: command
          command: echo "SECOND"
`
	if err := os.WriteFile(yamlPath, []byte(yaml), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	out := captureStdout(func() {
		rc := RunWithArgs([]string{yamlPath})
		if rc != 0 {
			t.Fatalf("non-zero exit: %d", rc)
		}
	})

	// ensure SECOND appears before FIRST in the output
	idx1 := strings.Index(out, "SECOND")
	idx2 := strings.Index(out, "FIRST")
	if idx1 == -1 || idx2 == -1 {
		t.Fatalf("expected both SECOND and FIRST in output, got: %s", out)
	}
	if idx1 > idx2 {
		t.Fatalf("expected SECOND before FIRST based on runs, got order: %s", out)
	}
}
