package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWhenDSLOperators(t *testing.T) {
	tmp := t.TempDir()

	// contains -> goto_step
	yaml1 := `pipeline:
  name: when-contains
  jobs:
    - name: j1
      steps:
        - name: s1
          type: command
          command: 'echo "hello world"'
          when:
            - contains: "hello"
              action: "goto_step"
              step: "s2"
        - name: s2
          type: command
          command: 'echo "CONTAINS_OK"'
`
	p1 := filepath.Join(tmp, "c1.yaml")
	if err := os.WriteFile(p1, []byte(yaml1), 0644); err != nil {
		t.Fatalf("write yaml1: %v", err)
	}
	out := captureStdout(func() {
		rc := RunWithArgs([]string{p1})
		if rc != 0 {
			t.Fatalf("non-zero exit: %d", rc)
		}
	})
	if !strings.Contains(out, "CONTAINS_OK") {
		t.Fatalf("expected CONTAINS_OK in output, got: %s", out)
	}

	// equals -> continue
	yaml2 := `pipeline:
  name: when-equals
  jobs:
    - name: j1
      steps:
        - name: s1
          type: command
          command: 'echo "EXACT"'
          when:
            - equals: "EXACT"
              action: "continue"
        - name: s2
          type: command
          command: 'echo "EQUALS_OK"'
`
	p2 := filepath.Join(tmp, "c2.yaml")
	if err := os.WriteFile(p2, []byte(yaml2), 0644); err != nil {
		t.Fatalf("write yaml2: %v", err)
	}
	out = captureStdout(func() {
		rc := RunWithArgs([]string{p2})
		if rc != 0 {
			t.Fatalf("non-zero exit: %d", rc)
		}
	})
	if !strings.Contains(out, "EQUALS_OK") {
		t.Fatalf("expected EQUALS_OK in output, got: %s", out)
	}

	// regex -> goto_step
	yaml3 := `pipeline:
  name: when-regex
  jobs:
    - name: j1
      steps:
        - name: s1
          type: command
          command: 'echo "val: 123"'
          when:
            - regex: "val: ([0-9]+)"
              action: "goto_step"
              step: "s2"
        - name: s2
          type: command
          command: 'echo "REGEX_OK"'
`
	p3 := filepath.Join(tmp, "c3.yaml")
	if err := os.WriteFile(p3, []byte(yaml3), 0644); err != nil {
		t.Fatalf("write yaml3: %v", err)
	}
	out = captureStdout(func() {
		rc := RunWithArgs([]string{p3})
		if rc != 0 {
			t.Fatalf("non-zero exit: %d", rc)
		}
	})
	if !strings.Contains(out, "REGEX_OK") {
		t.Fatalf("expected REGEX_OK in output, got: %s", out)
	}

	// exit_code -> goto_step
	yaml4 := `pipeline:
  name: when-exit
  jobs:
    - name: j1
      steps:
        - name: s1
          type: command
          command: 'bash -c "exit 42"'
          when:
            - exit_code: 42
              action: "goto_step"
              step: "s2"
        - name: s2
          type: command
          command: 'echo "EXIT42_OK"'
`
	p4 := filepath.Join(tmp, "c4.yaml")
	if err := os.WriteFile(p4, []byte(yaml4), 0644); err != nil {
		t.Fatalf("write yaml4: %v", err)
	}
	out = captureStdout(func() {
		rc := RunWithArgs([]string{p4})
		if rc != 0 {
			t.Fatalf("non-zero exit: %d", rc)
		}
	})
	if !strings.Contains(out, "EXIT42_OK") {
		t.Fatalf("expected EXIT42_OK in output, got: %s", out)
	}
}
