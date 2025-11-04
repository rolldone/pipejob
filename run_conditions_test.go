package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConditionGotoAndSaveOutput(t *testing.T) {
	tmp := t.TempDir()
	yamlPath := filepath.Join(tmp, "job.yaml")
	yaml := `pipeline:
  name: cond
  jobs:
    - name: j1
      steps:
        - name: step1
          type: command
          command: echo "alpha"
          save_output: out1
          conditions:
            - pattern: "alpha"
              action: "goto_step"
              step: "step3"
        - name: step2
          type: command
          command: echo "SHOULD_NOT_RUN"
        - name: step3
          type: command
          command: echo "GOT {{out1}}"
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

	if strings.Contains(out, "SHOULD_NOT_RUN") {
		t.Fatalf("expected step2 to be skipped, but output contains it: %s", out)
	}
	if !strings.Contains(out, "GOT alpha") {
		t.Fatalf("expected GOT alpha in output, got: %s", out)
	}
}
