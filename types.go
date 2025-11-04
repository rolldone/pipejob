package main

import (
	"strings"
)

// Minimal types matching the sample job YAML. We only support the fields
// required for local command execution.
type PipelineFile struct {
	Pipeline struct {
		Name      string            `yaml:"name"`
		Runs      []string          `yaml:"runs"`
		Variables map[string]string `yaml:"variables"`
		Jobs      []Job             `yaml:"jobs"`
	} `yaml:"pipeline"`
}

type Job struct {
	Name  string `yaml:"name"`
	Steps []Step `yaml:"steps"`
}

type Step struct {
	Name       string   `yaml:"name"`
	Type       string   `yaml:"type"`
	Command    string   `yaml:"command"`
	Commands   []string `yaml:"commands"`
	SaveOutput string   `yaml:"save_output"`
	Silent     bool     `yaml:"silent"`
	Conditions []struct {
		Pattern string `yaml:"pattern"`
		Action  string `yaml:"action"`
		Step    string `yaml:"step"`
		Job     string `yaml:"job"`
	} `yaml:"conditions"`
	// When is a more intuitive condition DSL: simple operators like contains,
	// equals, regex and exit_code. It is evaluated after the legacy
	// `conditions` patterns (kept for backward compatibility).
	When []struct {
		Contains string `yaml:"contains"`
		Equals   string `yaml:"equals"`
		Regex    string `yaml:"regex"`
		ExitCode *int   `yaml:"exit_code"`
		Action   string `yaml:"action"`
		Step     string `yaml:"step"`
		Job      string `yaml:"job"`
	} `yaml:"when"`
	ElseAction string `yaml:"else_action"`
	ElseStep   string `yaml:"else_step"`
	ElseJob    string `yaml:"else_job"`
	// optional timeout for the step, expressed as a Go duration string
	// (for example: "30s", "1m"). If set, the step's command will be
	// killed when the timeout is reached and treated as a non-zero exit.
	Timeout string `yaml:"timeout"`
	// optional idle timeout: maximum duration with no stdout/stderr activity
	// (for example: "30s", "1m"). If the command produces no output for
	// this duration the step is killed and treated as a timeout (exit 124).
	IdleTimeout string `yaml:"idle_timeout"`
	// on_timeout is a shortcut action applied when the step hits its timeout.
	// Supported values: continue, drop, goto_step, goto_job, fail
	OnTimeout     string `yaml:"on_timeout"`
	OnTimeoutStep string `yaml:"on_timeout_step"`
	OnTimeoutJob  string `yaml:"on_timeout_job"`
}

// helper to parse simple key=val CLI vars
type kvList []string

func (k *kvList) String() string { return strings.Join(*k, ",") }
func (k *kvList) Set(v string) error {
	*k = append(*k, v)
	return nil
}
