package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// runtimeShell holds an optional shell override (sh|cmd|powershell).
// When empty the runner auto-detects based on runtime.GOOS.
var runtimeShell string

// globalSilent when true suppresses per-step prints (command lines,
// stdout/stderr echoes, and inline per-step error prints). It is set by
// the global `--silent` flag.
var globalSilent bool

// Types and small helpers have been moved to types.go and helpers.go to keep
// this file focused on CLI and execution flow. See types.go for
// `PipelineFile`, `Job`, `Step`, and `kvList` definitions.

func main() {
	os.Exit(RunWithArgs(os.Args[1:]))
}

// RunWithArgs implements the CLI behavior and returns an exit code.
func RunWithArgs(args []string) (rc int) {
	// Pre-scan args so global flags like --var can appear anywhere (before
	// or after the positional YAML file). We extract supported flags and
	// return a cleaned args slice for positional handling.
	var cliVars kvList
	envFile := ".env"
	dryRun := false
	persistLogs := ""
	shellHint := "" // optional shell override: sh|cmd|powershell
	var defaultIdleTimeoutStr string

	cleaned := make([]string, 0, len(args))
	for i := 0; i < len(args); {
		a := args[i]
		// help flag anywhere should display usage and exit
		if a == "--help" || a == "-h" {
			printHelp()
			return 0
		}
		if strings.HasPrefix(a, "--var=") {
			cliVars.Set(strings.TrimPrefix(a, "--var="))
			i++
			continue
		}
		if a == "--var" {
			if i+1 < len(args) {
				cliVars.Set(args[i+1])
				i += 2
				continue
			}
			fmt.Fprintln(os.Stderr, "--var requires an argument")
			return 2
		}
		if strings.HasPrefix(a, "--env-file=") {
			envFile = strings.TrimPrefix(a, "--env-file=")
			i++
			continue
		}
		if a == "--env-file" {
			if i+1 < len(args) {
				envFile = args[i+1]
				i += 2
				continue
			}
			fmt.Fprintln(os.Stderr, "--env-file requires an argument")
			return 2
		}
		if a == "--dry-run" || strings.HasPrefix(a, "--dry-run=") {
			if a == "--dry-run" {
				dryRun = true
			} else {
				v := strings.TrimPrefix(a, "--dry-run=")
				dryRun = v != "false" && v != "0"
			}
			i++
			continue
		}
		if strings.HasPrefix(a, "--persist-logs=") {
			persistLogs = strings.TrimPrefix(a, "--persist-logs=")
			i++
			continue
		}
		if strings.HasPrefix(a, "--idle-timeout=") {
			defaultIdleTimeoutStr = strings.TrimPrefix(a, "--idle-timeout=")
			i++
			continue
		}
		if a == "--idle-timeout" {
			if i+1 < len(args) {
				defaultIdleTimeoutStr = args[i+1]
				i += 2
				continue
			}
			fmt.Fprintln(os.Stderr, "--idle-timeout requires an argument (Go duration, e.g. 2s)")
			return 2
		}
		if strings.HasPrefix(a, "--shell=") {
			shellHint = strings.TrimPrefix(a, "--shell=")
			i++
			continue
		}
		if a == "--shell" {
			if i+1 < len(args) {
				shellHint = args[i+1]
				i += 2
				continue
			}
			fmt.Fprintln(os.Stderr, "--shell requires an argument (sh|cmd|powershell)")
			return 2
		}
		if a == "--persist-logs" {
			if i+1 < len(args) {
				persistLogs = args[i+1]
				i += 2
				continue
			}
			fmt.Fprintln(os.Stderr, "--persist-logs requires an argument")
			return 2
		}
		if strings.HasPrefix(a, "--silent=") {
			v := strings.TrimPrefix(a, "--silent=")
			globalSilent = !(v == "false" || v == "0")
			i++
			continue
		}
		if a == "--silent" {
			globalSilent = true
			i++
			continue
		}
		// unknown or positional -> keep
		cleaned = append(cleaned, a)
		i++
	}

	// If subcommand 'new' is requested it will be the first cleaned arg.
	if len(cleaned) > 0 && cleaned[0] == "new" {
		newFs := flag.NewFlagSet("new", flag.ContinueOnError)
		var name string
		newFs.StringVar(&name, "name", "generated", "pipeline name")
		if err := newFs.Parse(cleaned[1:]); err != nil {
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

	if len(cleaned) == 0 {
		fmt.Fprintln(os.Stderr, "usage: pipejob <job.yaml> [flags]")
		return 2
	}
	yamlPath := cleaned[0]

	// expose shell hint to runLocalCommand via package-level variable
	runtimeShell = shellHint

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

	// Prepare temp workspace name. We avoid creating the temp dir or log
	// file unless needed to minimize IO. Logs are buffered in-memory and
	// only written to disk when (a) the user requested `--persist-logs` or
	// (b) the run exits non-zero (error) — this preserves logs by default on
	// error while avoiding disk writes on successful runs.
	ts := time.Now().Format("20060102-150405")
	tempBase := ".sync_temp"
	tempDir := filepath.Join(tempBase, "pipejob-"+ts)
	if persistLogs != "" {
		// use persist dir if requested; create it now so we can stream logs
		tempDir = persistLogs
		if err := os.MkdirAll(tempDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "failed to create persist dir %s: %v\n", tempDir, err)
			return 2
		}
	}

	// in-memory bounded log buffer (ring-like): we keep up to logCap
	// bytes of the most recent log output. This mimics the pipeline's
	// error-evidence buffer and avoids writing logs to disk on success.
	const logCap = 307200 // 300 KB
	var logBuf []byte
	logPath := filepath.Join(tempDir, "run.log")
	var lf *os.File
	if persistLogs != "" {
		f, err := os.Create(logPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create log file %s: %v\n", logPath, err)
			return 2
		}
		lf = f
		defer lf.Close()
	}

	// appendToBuf appends data to the in-memory buffer and truncates the
	// front if we exceed the capacity (keep the last logCap bytes).
	appendToBuf := func(b []byte) {
		if len(b) == 0 {
			return
		}
		logBuf = append(logBuf, b...)
		if len(logBuf) > logCap {
			// keep only the trailing logCap bytes
			logBuf = logBuf[len(logBuf)-logCap:]
		}
	}

	writeLog := func(s string) {
		line := []byte(s + "\n")
		appendToBuf(line)
		if lf != nil {
			lf.Write(line)
		}
	}

	// Cleanup / persist-on-error behavior: if the run exits non-zero and
	// the user didn't request `--persist-logs`, create the temp dir and
	// write the buffered log there so users can inspect failures. If the
	// run is successful we skip writing logs to avoid unnecessary IO.
	defer func() {
		// If user explicitly requested a persist dir, logs were already
		// written there and we don't remove them.
		if persistLogs != "" {
			return
		}
		if rc != 0 {
			// create temp dir and write buffered log
			if err := os.MkdirAll(tempDir, 0755); err != nil {
				fmt.Fprintf(os.Stderr, "failed to create temp dir for logs %s: %v\n", tempDir, err)
				return
			}
			lf2, err := os.Create(logPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to create log file %s: %v\n", logPath, err)
				return
			}
			// prepend an error-evidence header similar to pipeline logs
			header := []byte("=== ERROR EVIDENCE (last ~300KB) ===\n")
			_, _ = lf2.Write(header)
			_, _ = lf2.Write(logBuf)
			_ = lf2.Close()
			fmt.Fprintf(os.Stderr, "pipejob: logs preserved at %s\n", tempDir)
			return
		}
		// success case: do not write logs (save IO) and do nothing
	}()

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
			// parse optional step timeout once per step
			var stepTimeout time.Duration
			if step.Timeout != "" {
				d, perr := time.ParseDuration(step.Timeout)
				if perr != nil {
					msg := fmt.Sprintf("invalid timeout '%s' in step %s: %v", step.Timeout, step.Name, perr)
					fmt.Fprintln(os.Stderr, msg)
					writeLog(msg)
					return 6
				}
				stepTimeout = d
			}

			// parse optional idle timeout
			var stepIdleTimeout time.Duration
			// step-level idle_timeout takes precedence; otherwise use global default if provided
			if step.IdleTimeout != "" {
				d, perr := time.ParseDuration(step.IdleTimeout)
				if perr != nil {
					msg := fmt.Sprintf("invalid idle_timeout '%s' in step %s: %v", step.IdleTimeout, step.Name, perr)
					fmt.Fprintln(os.Stderr, msg)
					writeLog(msg)
					return 6
				}
				stepIdleTimeout = d
			} else if defaultIdleTimeoutStr != "" {
				d, perr := time.ParseDuration(defaultIdleTimeoutStr)
				if perr != nil {
					msg := fmt.Sprintf("invalid global --idle-timeout value '%s': %v", defaultIdleTimeoutStr, perr)
					fmt.Fprintln(os.Stderr, msg)
					writeLog(msg)
					return 6
				}
				stepIdleTimeout = d
			}

			for _, c := range cmds {
				rc := interpolate(c, vars)
				// Always print the command being executed so runs are traceable;
				// `silent` only hides the command output (stdout/stderr) and
				// inline per-step error messages, not the command itself.
				fmt.Printf("-> %s\n", rc)
				writeLog("CMD: " + rc)
				// capture output
				var outBuf bytes.Buffer
				exitCode, err := runLocalCommandExec(rc, stepTimeout, stepIdleTimeout, &outBuf, &outBuf)
				lastExitCode = exitCode
				if err != nil {
					msg := fmt.Sprintf("command failed: %v", err)
					if !(globalSilent || step.Silent) {
						fmt.Fprintln(os.Stderr, msg)
					}
					writeLog(msg)
					// don't immediately return: allow conditions to inspect exit code
					errOccurred = true
				}
				combinedOut.Write(outBuf.Bytes())
				// still echo to stdout for user visibility (unless silenced)
				if !(globalSilent || step.Silent) {
					os.Stdout.Write(outBuf.Bytes())
				}
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
						found, ok := resolveJobIndexExec(&execJobs, p.Pipeline.Jobs, cond.Job, ji)
						if !ok {
							msg := fmt.Sprintf("goto_job target '%s' not found", cond.Job)
							fmt.Fprintln(os.Stderr, msg)
							writeLog(msg)
							return 6
						}
						ji = found - 1 // outer loop will increment
						// insert a resume job so we continue remaining steps after
						// the target job completes
						insertResumeJob(&execJobs, found, *job, si)
						// exit current job's steps immediately
						si = len(job.Steps)
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
							found, ok := resolveJobIndexExec(&execJobs, p.Pipeline.Jobs, w.Job, ji)
							if !ok {
								msg := fmt.Sprintf("goto_job target '%s' not found", w.Job)
								fmt.Fprintln(os.Stderr, msg)
								writeLog(msg)
								return 6
							}
							ji = found - 1
							// insert resume job so remaining steps are run after the target
							insertResumeJob(&execJobs, found, *job, si)
							// exit current job's steps immediately
							si = len(job.Steps)
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
				// else_action present; proceed to handle it
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
					found, ok := resolveJobIndexExec(&execJobs, p.Pipeline.Jobs, step.ElseJob, ji)
					if !ok {
						msg := fmt.Sprintf("else goto_job target '%s' not found", step.ElseJob)
						fmt.Fprintln(os.Stderr, msg)
						writeLog(msg)
						return 6
					}
					ji = found - 1
					// insert resume job so remaining steps are run after the target
					insertResumeJob(&execJobs, found, *job, si)
					// exit current job's steps immediately
					si = len(job.Steps)
				case "fail":
					msg := fmt.Sprintf("step %s failed due to else_action", step.Name)
					if !(globalSilent || step.Silent) {
						fmt.Fprintln(os.Stderr, msg)
					}
					writeLog(msg)
					return 7
				default:
					msg := fmt.Sprintf("unknown else_action '%s' in step %s", step.ElseAction, step.Name)
					fmt.Fprintln(os.Stderr, msg)
					writeLog(msg)
					return 6
				}
			}
			// mark else_action as handled so the default non-zero handling doesn't fire
			conditionMatched = true

			// If a timeout happened and user supplied an on_timeout shortcut, handle it
			if errOccurred && lastExitCode == 124 && step.OnTimeout != "" && !conditionMatched {
				switch step.OnTimeout {
				case "continue":
					// nothing, proceed
				case "drop":
					writeLog("on_timeout: drop")
					return 0
				case "goto_step":
					if step.OnTimeoutStep == "" {
						msg := fmt.Sprintf("on_timeout goto_step requires 'on_timeout_step' in step %s", step.Name)
						fmt.Fprintln(os.Stderr, msg)
						writeLog(msg)
						return 6
					}
					idx, ok := stepIndex[step.OnTimeoutStep]
					if !ok {
						msg := fmt.Sprintf("on_timeout goto_step target '%s' not found in job %s", step.OnTimeoutStep, job.Name)
						fmt.Fprintln(os.Stderr, msg)
						writeLog(msg)
						return 6
					}
					si = idx - 1
				case "goto_job":
					if step.OnTimeoutJob == "" {
						msg := fmt.Sprintf("on_timeout goto_job requires 'on_timeout_job' in step %s", step.Name)
						fmt.Fprintln(os.Stderr, msg)
						writeLog(msg)
						return 6
					}
					found, ok := resolveJobIndexExec(&execJobs, p.Pipeline.Jobs, step.OnTimeoutJob, ji)
					if !ok {
						msg := fmt.Sprintf("on_timeout goto_job target '%s' not found", step.OnTimeoutJob)
						fmt.Fprintln(os.Stderr, msg)
						writeLog(msg)
						return 6
					}
					ji = found - 1
					// insert resume job so remaining steps are run after the target
					insertResumeJob(&execJobs, found, *job, si)
					// exit current job's steps immediately
					si = len(job.Steps)
				case "fail":
					msg := fmt.Sprintf("step %s timed out", step.Name)
					if !(globalSilent || step.Silent) {
						fmt.Fprintln(os.Stderr, msg)
					}
					writeLog(msg)
					return 7
				default:
					msg := fmt.Sprintf("unknown on_timeout action '%s' in step %s", step.OnTimeout, step.Name)
					fmt.Fprintln(os.Stderr, msg)
					writeLog(msg)
					return 6
				}
				// mark as handled so the default non-zero handling doesn't fire
				conditionMatched = true
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

	// On success we avoid printing the log path to prevent confusion when the
	// temporary workspace is cleaned up. Errors still print messages to stderr.
	writeLog("completed")
	return 0
}

// insertResumeJob inserts a copy of `job` containing only steps after
// `resumeFrom` into execJobs immediately after index `after`. The new job
// has a generated unique name so it will be executed exactly once.
func insertResumeJob(execJobs *[]Job, after int, job Job, resumeFrom int) {
	if resumeFrom+1 >= len(job.Steps) {
		return
	}
	// copy remaining steps
	rem := make([]Step, len(job.Steps[resumeFrom+1:]))
	copy(rem, job.Steps[resumeFrom+1:])
	newJob := Job{
		Name:  job.Name + "-resume-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Steps: rem,
	}
	pos := after + 1
	if pos < 0 {
		pos = 0
	}
	if pos > len(*execJobs) {
		pos = len(*execJobs)
	}
	*execJobs = append((*execJobs)[:pos], append([]Job{newJob}, (*execJobs)[pos:]...)...)
}

// parseEnvFile and interpolate were moved to helpers.go during the
// refactor to keep main.go focused on CLI and flow.

// resolveJobIndex and runLocalCommand were moved to exec.go as
// resolveJobIndexExec and runLocalCommandExec respectively.

// printHelp prints a short usage message describing global flags and
// subcommands. It's invoked when the user passes -h or --help anywhere on
// the command line.
func printHelp() {
	fmt.Println("Usage: pipejob <job.yaml> [flags]")
	fmt.Println()
	fmt.Println("Global flags:")
	fmt.Println("  --env-file PATH      Path to .env file (default: .env)")
	fmt.Println("  --var KEY=VAL        Set a variable (repeatable). Flags can appear anywhere")
	fmt.Println("  --dry-run            Render commands without executing them")
	fmt.Println("  --persist-logs DIR   Stream logs live to DIR (keeps logs)")
	fmt.Println("  --idle-timeout D     Global idle timeout for steps with no output (Go duration, e.g. 2s). Step-level idle_timeout overrides this. Default: 0s (disabled)")
	fmt.Println("  --shell <sh|cmd|powershell>  Override shell used to run commands")
	fmt.Println("  --silent             Suppress per-step prints (command lines and stdout/stderr echoes)")
	fmt.Println()
	fmt.Println("Subcommands:")
	fmt.Println("  new <out.yaml>       Generate a minimal example pipeline YAML")
	fmt.Println()
	fmt.Println("Notes:")
	fmt.Println("  - Flags are positional-agnostic: they can appear before or after the YAML file.")
	fmt.Println("  - Use --persist-logs if you need full logs even on successful runs.")
}
