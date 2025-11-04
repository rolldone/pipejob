package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
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
}

// helper to parse simple key=val CLI vars
type kvList []string

func (k *kvList) String() string { return strings.Join(*k, ",") }
func (k *kvList) Set(v string) error {
	*k = append(*k, v)
	return nil
}

func main() {
	os.Exit(RunWithArgs(os.Args[1:]))
}

// RunWithArgs implements the CLI behavior and returns an exit code.
func RunWithArgs(args []string) int {
	// support a simple generator: `pipejob new <out.yaml>`
	if len(args) > 0 && args[0] == "new" {
		newFs := flag.NewFlagSet("new", flag.ContinueOnError)
		var name string
		newFs.StringVar(&name, "name", "generated", "pipeline name")
		if err := newFs.Parse(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if newFs.NArg() == 0 {
			fmt.Fprintln(os.Stderr, "usage: pipejob new <out.yaml>")
			return 2
		}
		outPath := newFs.Arg(0)
		// build a minimal pipeline
		gen := PipelineFile{}
		gen.Pipeline.Name = name
		gen.Pipeline.Variables = map[string]string{"EXAMPLE": "value"}
		gen.Pipeline.Jobs = []Job{{Name: "job1", Steps: []Step{{Name: "step1", Type: "command", Command: "echo 'hello world'"}}}}
		data, err := yaml.Marshal(&gen)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to marshal generated yaml: %v\n", err)
			return 2
		}
		if err := os.WriteFile(outPath, data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "failed to write %s: %v\n", outPath, err)
			return 2
		}
		fmt.Printf("generated %s\n", outPath)
		return 0
	}
	var cliVars kvList
	var envFile string
	var dryRun bool
	var persistLogs string

	fs := flag.NewFlagSet("pipejob", flag.ContinueOnError)
	fs.Var(&cliVars, "var", "key=val variable to render (repeatable)")
	fs.StringVar(&envFile, "env-file", ".env", "path to .env file (optional)")
	fs.BoolVar(&dryRun, "dry-run", false, "validate and print steps without executing")
	fs.StringVar(&persistLogs, "persist-logs", "", "directory to persist logs (optional)")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: pipejob <job.yaml> [flags]")
		return 2
	}
	yamlPath := fs.Arg(0)

	// Read YAML
	b, err := os.ReadFile(yamlPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read %s: %v\n", yamlPath, err)
		return 2
	}

	// Quick check: reject if there is an `execution` with mode: live to be safe.
	var root map[string]interface{}
	if err := yaml.Unmarshal(b, &root); err == nil {
		if execVal, ok := root["execution"]; ok {
			if em, ok2 := execVal.(map[string]interface{}); ok2 {
				if mm, ok3 := em["mode"]; ok3 {
					if ms, ok4 := mm.(string); ok4 && strings.ToLower(ms) == "live" {
						fmt.Fprintln(os.Stderr, "pipejob: refusing to run pipeline with execution.mode=live in minimal local tool")
						return 3
					}
				}
			}

		}
	}

	var p PipelineFile
	if err := yaml.Unmarshal(b, &p); err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse yaml %s: %v\n", yamlPath, err)
		return 2
	}

	// Build variables: pipeline vars -> env file -> CLI vars (CLI highest precedence)
	vars := map[string]string{}
	for k, v := range p.Pipeline.Variables {
		vars[k] = v
	}

	// load .env if present
	if envFile != "" {
		if efVars, err := parseEnvFile(envFile); err == nil {
			for k, v := range efVars {
				vars[k] = v
			}
		}
	}

	// apply CLI vars (key=val)
	for _, kv := range cliVars {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "invalid --var value: %s (expected key=val)\n", kv)
			return 2
		}
		vars[parts[0]] = parts[1]
	}

	// Prepare temp workspace
	ts := time.Now().Format("20060102-150405")
	tempBase := ".sync_temp"
	tempDir := filepath.Join(tempBase, "pipejob-"+ts)
	if persistLogs != "" {
		// use persist dir if requested
		tempDir = persistLogs
	}
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir %s: %v\n", tempDir, err)
		return 2
	}

	// simple run log
	logPath := filepath.Join(tempDir, "run.log")
	lf, _ := os.Create(logPath)
	defer lf.Close()
	writeLog := func(s string) {
		lf.WriteString(s + "\n")
	}

	// On exit, cleanup unless persistLogs provided
	if persistLogs == "" {
		defer func() {
			_ = os.RemoveAll(tempDir)
		}()
	}

	// dry-run: print rendered steps and exit
	if dryRun {
		fmt.Printf("Pipeline: %s\n", p.Pipeline.Name)
		for _, job := range p.Pipeline.Jobs {
			fmt.Printf("Job: %s\n", job.Name)
			for _, step := range job.Steps {
				if strings.ToLower(step.Type) != "command" && step.Type != "" {
					fmt.Printf("  step %s: unsupported step type '%s' (would abort)\n", step.Name, step.Type)
					continue
				}
				cmds := step.Commands
				if len(cmds) == 0 && step.Command != "" {
					cmds = []string{step.Command}
				}
				for _, c := range cmds {
					rc := interpolate(c, vars)
					fmt.Printf("  %s\n", rc)
				}
			}
		}
		return 0
	}

	// Determine job execution order. If Pipeline.Runs is provided, use that
	// order. Otherwise use the order jobs are declared in the YAML.
	var execJobs []Job
	if len(p.Pipeline.Runs) > 0 {
		// Build a name->job map
		jm := map[string]Job{}
		for _, j := range p.Pipeline.Jobs {
			jm[j.Name] = j
		}
		for _, name := range p.Pipeline.Runs {
			j, ok := jm[name]
			if !ok {
				msg := fmt.Sprintf("runs lists unknown job '%s' - aborting", name)
				fmt.Fprintln(os.Stderr, msg)
				writeLog(msg)
				return 6
			}
			execJobs = append(execJobs, j)
		}
	} else {
		execJobs = p.Pipeline.Jobs
	}

	// Execute each command step sequentially with condition support.
	// Use index-based loops so we can implement goto_step and goto_job.
	for ji := 0; ji < len(execJobs); ji++ {
		job := &execJobs[ji]
		fmt.Printf("== Job: %s ==\n", job.Name)
		writeLog(fmt.Sprintf("== Job: %s ==", job.Name))
		// Build step name -> index map for goto_step resolution
		stepIndex := map[string]int{}
		for i, s := range job.Steps {
			stepIndex[s.Name] = i
		}

		for si := 0; si < len(job.Steps); si++ {
			step := &job.Steps[si]
			if strings.ToLower(step.Type) != "command" && step.Type != "" {
				msg := fmt.Sprintf("unsupported step type '%s' in step '%s' - aborting", step.Type, step.Name)
				fmt.Fprintln(os.Stderr, msg)
				writeLog(msg)
				return 4
			}
			cmds := step.Commands
			if len(cmds) == 0 && step.Command != "" {
				cmds = []string{step.Command}
			}

			// run each command and capture combined output
			var combinedOut strings.Builder
			lastExitCode := 0
			errOccurred := false
			for _, c := range cmds {
				rc := interpolate(c, vars)
				fmt.Printf("-> %s\n", rc)
				writeLog("CMD: " + rc)
				// capture output
				var outBuf bytes.Buffer
				exitCode, err := runLocalCommand(rc, &outBuf, &outBuf)
				lastExitCode = exitCode
				if err != nil {
					msg := fmt.Sprintf("command failed: %v", err)
					fmt.Fprintln(os.Stderr, msg)
					writeLog(msg)
					// don't immediately return: allow conditions to inspect exit code
					errOccurred = true
				}
				combinedOut.Write(outBuf.Bytes())
				// still echo to stdout for user visibility
				os.Stdout.Write(outBuf.Bytes())
			}

			outStr := combinedOut.String()
			// save output if requested
			if step.SaveOutput != "" {
				vars[step.SaveOutput] = strings.TrimSpace(outStr)
			}

			// Evaluate conditions
			conditionMatched := false
			// legacy `conditions` (pattern -> action)
			for _, cond := range step.Conditions {
				pat := interpolate(cond.Pattern, vars)
				re, err := regexp.Compile(pat)
				if err != nil {
					msg := fmt.Sprintf("invalid condition regex '%s' in step %s: %v", pat, step.Name, err)
					fmt.Fprintln(os.Stderr, msg)
					writeLog(msg)
					return 6
				}
				if re.MatchString(outStr) {
					conditionMatched = true
					switch cond.Action {
					case "continue":
						// do nothing, proceed to next step
					case "drop":
						writeLog("condition matched: drop")
						return 0
					case "goto_step":
						if cond.Step == "" {
							msg := fmt.Sprintf("goto_step requires 'step' in step %s", step.Name)
							fmt.Fprintln(os.Stderr, msg)
							writeLog(msg)
							return 6
						}
						idx, ok := stepIndex[cond.Step]
						if !ok {
							msg := fmt.Sprintf("goto_step target '%s' not found in job %s", cond.Step, job.Name)
							fmt.Fprintln(os.Stderr, msg)
							writeLog(msg)
							return 6
						}
						si = idx - 1 // -1 because loop will increment
					case "goto_job":
						if cond.Job == "" {
							msg := fmt.Sprintf("goto_job requires 'job' in step %s", step.Name)
							fmt.Fprintln(os.Stderr, msg)
							writeLog(msg)
							return 6
						}
						// find job index in execJobs
						found := -1
						for k := range execJobs {
							if execJobs[k].Name == cond.Job {
								found = k
								break
							}
						}
						if found == -1 {
							msg := fmt.Sprintf("goto_job target '%s' not found", cond.Job)
							fmt.Fprintln(os.Stderr, msg)
							writeLog(msg)
							return 6
						}
						ji = found - 1 // outer loop will increment
					case "fail":
						msg := fmt.Sprintf("step %s failed due to condition match", step.Name)
						fmt.Fprintln(os.Stderr, msg)
						writeLog(msg)
						return 7
					default:
						msg := fmt.Sprintf("unknown condition action '%s' in step %s", cond.Action, step.Name)
						fmt.Fprintln(os.Stderr, msg)
						writeLog(msg)
						return 6
					}
					// if we performed goto_job we broke the switch and must break cond loop
				}
			}

			// new `when` DSL - simpler operators. Evaluated after legacy conditions.
			if !conditionMatched {
				for _, w := range step.When {
					match := false
					// evaluate operators with interpolation
					if w.Contains != "" {
						if strings.Contains(outStr, interpolate(w.Contains, vars)) {
							match = true
						}
					}
					if !match && w.Equals != "" {
						if strings.TrimSpace(outStr) == strings.TrimSpace(interpolate(w.Equals, vars)) {
							match = true
						}
					}
					if !match && w.Regex != "" {
						pat := interpolate(w.Regex, vars)
						re, err := regexp.Compile(pat)
						if err != nil {
							msg := fmt.Sprintf("invalid when.regex '%s' in step %s: %v", pat, step.Name, err)
							fmt.Fprintln(os.Stderr, msg)
							writeLog(msg)
							return 6
						}
						if re.MatchString(outStr) {
							match = true
						}
					}
					if !match && w.ExitCode != nil {
						if lastExitCode == *w.ExitCode {
							match = true
						}
					}

					if match {
						conditionMatched = true
						switch w.Action {
						case "continue":
						case "drop":
							writeLog("when matched: drop")
							return 0
						case "goto_step":
							if w.Step == "" {
								msg := fmt.Sprintf("goto_step requires 'step' in step %s", step.Name)
								fmt.Fprintln(os.Stderr, msg)
								writeLog(msg)
								return 6
							}
							idx, ok := stepIndex[w.Step]
							if !ok {
								msg := fmt.Sprintf("goto_step target '%s' not found in job %s", w.Step, job.Name)
								fmt.Fprintln(os.Stderr, msg)
								writeLog(msg)
								return 6
							}
							si = idx - 1
						case "goto_job":
							if w.Job == "" {
								msg := fmt.Sprintf("goto_job requires 'job' in step %s", step.Name)
								fmt.Fprintln(os.Stderr, msg)
								writeLog(msg)
								return 6
							}
							found := -1
							for k := range execJobs {
								if execJobs[k].Name == w.Job {
									found = k
									break
								}
							}
							if found == -1 {
								msg := fmt.Sprintf("goto_job target '%s' not found", w.Job)
								fmt.Fprintln(os.Stderr, msg)
								writeLog(msg)
								return 6
							}
							ji = found - 1
						case "fail":
							msg := fmt.Sprintf("step %s failed due to when match", step.Name)
							fmt.Fprintln(os.Stderr, msg)
							writeLog(msg)
							return 7
						default:
							msg := fmt.Sprintf("unknown when action '%s' in step %s", w.Action, step.Name)
							fmt.Fprintln(os.Stderr, msg)
							writeLog(msg)
							return 6
						}
						break
					}
				}
			}
			if !conditionMatched && step.ElseAction != "" {
				switch step.ElseAction {
				case "continue":
					// nothing
				case "drop":
					writeLog("else_action: drop")
					return 0
				case "goto_step":
					if step.ElseStep == "" {
						msg := fmt.Sprintf("else goto_step requires 'else_step' in step %s", step.Name)
						fmt.Fprintln(os.Stderr, msg)
						writeLog(msg)
						return 6
					}
					idx, ok := stepIndex[step.ElseStep]
					if !ok {
						msg := fmt.Sprintf("else goto_step target '%s' not found in job %s", step.ElseStep, job.Name)
						fmt.Fprintln(os.Stderr, msg)
						writeLog(msg)
						return 6
					}
					si = idx - 1
				case "goto_job":
					if step.ElseJob == "" {
						msg := fmt.Sprintf("else goto_job requires 'else_job' in step %s", step.Name)
						fmt.Fprintln(os.Stderr, msg)
						writeLog(msg)
						return 6
					}
					found := -1
					for k := range execJobs {
						if execJobs[k].Name == step.ElseJob {
							found = k
							break
						}
					}
					if found == -1 {
						msg := fmt.Sprintf("else goto_job target '%s' not found", step.ElseJob)
						fmt.Fprintln(os.Stderr, msg)
						writeLog(msg)
						return 6
					}
					ji = found - 1
				case "fail":
					msg := fmt.Sprintf("step %s failed due to else_action", step.Name)
					fmt.Fprintln(os.Stderr, msg)
					writeLog(msg)
					return 7
				default:
					msg := fmt.Sprintf("unknown else_action '%s' in step %s", step.ElseAction, step.Name)
					fmt.Fprintln(os.Stderr, msg)
					writeLog(msg)
					return 6
				}
			}

			// If no condition matched and a command returned non-zero, treat as failure
			if !conditionMatched && errOccurred {
				msg := fmt.Sprintf("step %s command(s) returned non-zero exit and no condition matched", step.Name)
				fmt.Fprintln(os.Stderr, msg)
				writeLog(msg)
				return 5
			}
		}
	}

	fmt.Printf("pipejob: completed successfully (logs: %s)\n", logPath)
	writeLog("completed")
	return 0
}

func parseEnvFile(path string) (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// strip surrounding quotes if present
		if strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"") {
			val = strings.Trim(val, "\"")
		}
		out[key] = val
	}
	if scanner.Err() != nil {
		return nil, scanner.Err()
	}
	return out, nil
}

func interpolate(tmpl string, vars map[string]string) string {
	if tmpl == "" {
		return tmpl
	}
	res := tmpl
	for k, v := range vars {
		// support {{KEY}} and {{ KEY }} and {{.KEY}}
		res = strings.ReplaceAll(res, "{{"+k+"}}", v)
		res = strings.ReplaceAll(res, "{{ "+k+" }}", v)
		res = strings.ReplaceAll(res, "{{."+k+"}}", v)
		res = strings.ReplaceAll(res, "{{ ."+k+" }}", v)
	}
	return res
}

// runLocalCommand runs the given command line under /bin/sh -lc and returns
// the process exit code and an error (if any). Exit code is 0 on success.
func runLocalCommand(cmdLine string, stdout io.Writer, stderr io.Writer) (int, error) {
	cmd := exec.Command("/bin/sh", "-lc", cmdLine)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	// try to extract exit code from *exec.ExitError
	if ee, ok := err.(*exec.ExitError); ok {
		if status, ok2 := ee.Sys().(interface{ ExitStatus() int }); ok2 {
			return status.ExitStatus(), err
		}
	}
	// fallback to generic non-zero
	return 1, err
}
