package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureOutput(f func()) string {
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

func TestRunSimpleEcho(t *testing.T) {
	tmpdir := t.TempDir()
	yamlPath := filepath.Join(tmpdir, "job.yaml")
	yaml := `pipeline:
  name: test
  variables:
    MSG: world
  jobs:
    - name: run
      steps:
        - name: say
          type: command
          command: echo "hello {{MSG}}"
`
	if err := os.WriteFile(yamlPath, []byte(yaml), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	out := captureOutput(func() {
		rc := RunWithArgs([]string{yamlPath})
		if rc != 0 {
			t.Fatalf("non-zero exit: %d", rc)
		}
	})

	if !strings.Contains(out, "hello world") {
		t.Fatalf("expected hello world in output, got: %s", out)
	}
}
