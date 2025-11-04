# pipejob — minimal local pipeline runner

This is a tiny, local-only helper to run pipeline job YAML files (the same structure as `job-sample.yaml`) without touching the main `pipeline` app.

Note: `pipejob` is an intentionally small, simplified, local-only subset of the full
`pipeline` project. It is meant for quick local runs and testing. For the full
feature set (SSH execution, agents, file sync, advanced logging and orchestration),
use the main project at: https://github.com/rolldone/pipeline

Goals:
- Run `command` steps locally (via `/bin/sh -lc`).
- Support variable injection from a `.env` file and `--var` CLI flags.
- Keep behavior simple and safe: no SSH/agent/file-sync support and `execution.mode=live` is refused.

Quickstart

Build:

```bash
cd sub_app/pipejob
go build -o pipejob
```

Usage:

```bash
./pipejob job.yaml [--env-file .env] [--var KEY=VAL] [--dry-run] [--persist-logs DIR]
```

Behavior:
- Variables precedence: pipeline YAML variables < `.env` file < `--var` flags.
- `--dry-run` will render and print the commands without executing them.
- By default a temporary workspace `.sync_temp/pipejob-<timestamp>` is created and removed on success; use `--persist-logs DIR` to keep logs/artifacts.

Limitations:
- Only `type: command` steps are supported. Other step types will cause the run to abort with an error.
- `execution.mode=live` is rejected for safety.

This tool is intentionally minimal and designed for local adhoc usage. If you want richer behavior (hosts, remote execution, agents), use the main `pipeline` application.

When condition DSL
------------------

`pipejob` supports a small, easy-to-read `when` condition DSL on steps in addition to the legacy `conditions` regex form. The `when` block accepts simple operators and maps them to the same actions used by `conditions` (continue, drop, goto_step, goto_job, fail).

Supported operators:
- `contains`: substring match against the saved command output
- `equals`: trimmed exact equality against the saved command output
- `regex`: RE2 regular expression match
- `exit_code`: integer match against the command's exit code

Examples:

1) contains → goto_step

```yaml
steps:
  - name: check
    type: command
    command: 'echo "hello world"'
    when:
      - contains: "hello"
        action: "goto_step"
        step: "success"
  - name: success
    type: command
    command: 'echo "CONTAINS_OK"'
```

2) equals → continue

```yaml
steps:
  - name: exact
    type: command
    command: 'echo "EXACT"'
    when:
      - equals: "EXACT"
        action: "continue"
  - name: next
    type: command
    command: 'echo "EQUALS_OK"'
```

3) regex and exit_code examples

```yaml
steps:
  - name: regex
    type: command
    command: 'echo "val: 123"'
    when:
      - regex: "val: ([0-9]+)"
        action: "goto_step"
        step: "got"
  - name: got
    type: command
    command: 'echo "REGEX_OK"'

  - name: exitcheck
    type: command
    command: 'bash -c "exit 42"'
    when:
      - exit_code: 42
        action: "goto_step"
        step: "exit_ok"
  - name: exit_ok
    type: command
    command: 'echo "EXIT42_OK"'
```

Notes:
- Legacy `conditions` (pattern + action) are still supported and evaluated first for backward compatibility; `when` is evaluated after. If you prefer `when` to be primary we can flip the order in a follow-up.
- `when` values are interpolated using the same `{{VAR}}` rules before evaluation (e.g. `contains: "{{OUT}}"`).
- `exit_code` matches the last command's exit code when a step runs multiple commands.

Legacy `conditions`
-------------------

The older `conditions` form (regex `pattern` + `action`) is still supported and is evaluated before the `when` block for backward compatibility. Here's a small example showing common actions:

```yaml
steps:
  - name: parse
    type: command
    command: ./parse.sh
    conditions:
      - pattern: 'OK$'
        action: continue
      - pattern: 'STOP'
        action: drop   # stop the pipeline and return success
      - pattern: 'ERR'
        action: fail   # stop the pipeline and return failure
```

Drop vs Fail
------------
- `drop` immediately ends the run and returns exit code 0 (success).
- `fail` immediately ends the run and returns a non-zero exit code (currently 7).

Log behavior
------------
By default `pipejob` creates a temporary workspace (`.sync_temp/pipejob-<timestamp>`) and removes it on success. That means a successful run that used `drop` may not leave an inspectable `run.log` unless you specify `--persist-logs DIR`. Use `--persist-logs` to keep the temp workspace or a directory of your choice for debugging.

Error‑evidence buffer (IO‑sparing behavior)
-----------------------------------------

To reduce disk I/O for successful local runs, `pipejob` keeps recent output in an in‑memory bounded buffer (approximately 300 KB). The runner only writes that buffer to disk when one of the following happens:

- the run exits with a non‑zero status (an error occurred), or
- the user explicitly passes `--persist-logs DIR` (logs are written live to the specified directory).

When an error causes the buffer to be flushed, `pipejob` creates a small temp workspace under `.sync_temp/pipejob-<timestamp>/` and writes `run.log` containing an "ERROR EVIDENCE" header followed by the last ~300 KB of output. It also prints a short notification to stderr, for example:

```
pipejob: logs preserved at .sync_temp/pipejob-20251104-072132
```

This mirrors the behaviour used in the main `pipeline` tool: keep an in‑memory history for debugging, avoid writing logs for the common successful case, and preserve recent output only when debugging is needed.

If you want logs regardless of success/failure, use `--persist-logs DIR` to stream logs live into a directory you control.

Silent printing (per-step and global)
------------------------------------

`pipejob` supports silencing noisy steps in two ways:

-- Per-step: set `silent: true` on a step to suppress printing the command output (stdout/stderr) and per-step inline error messages while still recording logs in the runner's in‑memory buffer (flushed on error). The command being executed (the `-> <cmd>` line) is still printed so runs are traceable.

-- Global flag: pass `--silent` to the `pipejob` command to suppress per-step command output (stdout/stderr) and per-step inline error prints by default. The command lines (`-> <cmd>`) remain visible so you can trace which commands ran. Job headers and critical runner errors (invalid YAML/conditions, missing targets, etc.) still print so you can diagnose failures.

Examples:

```bash
./pipejob --silent examples/sim-continue.yaml
# or place the flag after the YAML file; flags are positional-agnostic:
./pipejob examples/sim-continue.yaml --silent
```

Per-step example (keep the global default but silence a single step):

```yaml
steps:
  - name: noisy
    type: command
    command: ./generate-lots-of-output.sh
    silent: true

  - name: next
    type: command
    command: echo done
```

Notes:
- `--silent` is a convenience to quickly hide noisy step output during local runs; if you want a mixed policy (some steps silent, some noisy) omit `--silent` and use the per-step `silent: true` only on the noisy steps.
- If you still need full logs while running silently, use `--persist-logs DIR` to stream logs to disk.

Timeouts
--------

`pipejob` supports an optional per-step `timeout` field. Specify a Go duration string (for example `"30s"`, `"1m"`) and the step's command will be killed when the timeout is reached and treated as a non-zero exit (exit code 124).

Example:

```yaml
steps:
  - name: maybe-slow
    type: command
    command: "sleep 10"
    timeout: "3s"

  - name: next
    type: command
    command: "echo continued"
```

Additional examples: on_timeout shortcuts
---------------------------------------

You can use the `on_timeout` shortcut to pick an action when a step hits its timeout. Two common patterns are shown below.

1) Jump to a recovery step in the same job:

```yaml
steps:
  - name: slow
    type: command
    command: "sleep 10"
    timeout: "2s"
    on_timeout: "goto_step"
    on_timeout_step: "recover"

  - name: recover
    type: command
    command: "echo recovered after timeout"
```

2) Jump to another job in the pipeline:

```yaml
pipeline:
  runs: [job1, job2]
jobs:
  - name: job1
    steps:
      - name: slow
        type: command
        command: "sleep 10"
        timeout: "2s"
        on_timeout: "goto_job"
        on_timeout_job: "job2"

  - name: job2
    steps:
      - name: recovery
        type: command
        command: "echo recovery job2 - jumped from job1 on timeout"
```

Notes:
- Timeouts are per-step and apply to the whole step (if a step has multiple `commands`, the timeout applies across the sequence as it's parsed per-step).
- If a timeout occurs the runner treats it as a non-zero exit (exit code 124) and normal `when`/`conditions`/`else_action` evaluation still applies.
 - Implemented with context cancellation; on some systems child processes may survive if they spawn backgrounded descendants. If you need strict process-group killing we can improve the implementation to set process groups and kill them on timeout.

 - Idle timeout: `idle_timeout` is supported per-step (Go duration string). Default is `0s` (disabled). If a command produces no stdout/stderr activity for the
   specified duration the runner will kill the process (exit code 124) and normal `when`/`conditions`/`on_timeout` handling applies. See
   `examples/idle-timeout.yaml` for a demonstration where a long `sleep` with `idle_timeout: "2s"` jumps to a recovery step.
  
   On Unix the runner kills the process group (so backgrounded children are also terminated). On Windows the runner will attempt to
   terminate the process tree using `taskkill /T /F <PID>`; this may require appropriate privileges on some systems.

Shell / Windows behaviour
-------------------------

`pipejob` auto-detects the host OS and uses a sensible default shell:

- On Windows the runner uses `cmd.exe /C` by default.
- On Unix-like systems it uses `/bin/sh -lc`.

If you need to override the default shell (for example to use PowerShell on Windows), use the `--shell` flag. Supported values: `sh`, `cmd`, `powershell`.

Examples:

```bash
# force PowerShell (Windows)
./pipejob --shell powershell job.yaml

# force cmd (Windows) or rely on auto-detect
./pipejob --shell cmd job.yaml

# POSIX shell (default on Linux/macOS)
./pipejob --shell sh job.yaml
```

Notes:
- The YAML step commands are executed by the chosen shell, so commands must be compatible with that shell (e.g., `rm` is POSIX, `del` is cmd). `pipejob` does not translate commands across shells.
- `--shell` is a global flag and can appear anywhere on the command line (before or after the job YAML).

else_action and how it relates to conditions/when
-----------------------------------------------

Each step may optionally declare an `else_action` (and target fields `else_step` / `else_job`) which is used when none of the `conditions` or `when` rules matched. The evaluation order is:

1. legacy `conditions` (regex patterns)
2. `when` DSL rules (contains/equals/regex/exit_code)
3. `else_action` if no condition matched

This makes `else_action` useful for defining a default path when a command didn't produce any matched outputs or the exit code didn't match any `when` rules. Supported values are the same actions used by `conditions`/`when`:

- `continue` — proceed to the next step
- `drop` — stop the pipeline and return success (exit code 0)
- `goto_step` — jump to another step in the same job (requires `else_step`)
- `goto_job` — jump to another job in the pipeline (requires `else_job`)
- `fail` — stop the pipeline and return failure (currently exit code 7)

Examples

1) Default to dropping (success) when nothing matched:

```yaml
steps:
  - name: maybe
    type: command
    command: ls vadfvfv
    when:
      - exit_code: 0
        action: continue
    else_action: drop   # if ls fails (non-zero) and no when matched -> treat as success
```

2) Default to failing but jump to recovery step if specified:

```yaml
steps:
  - name: check
    type: command
    command: ./check.sh
    when:
      - contains: "OK"
        action: continue
    else_action: goto_step
    else_step: recover

  - name: recover
    type: command
    command: ./recover.sh
```

Notes:
- `else_action` only runs when no `conditions` or `when` entries matched the output/exit code.
- Use `else_step` or `else_job` to provide targets for `goto_step` or `goto_job`.
- Because `drop` returns success and the default behavior removes temporary logs on success, you may want to use `--persist-logs` when you expect to inspect artifacts from a dropped run.

goto_step, goto_job and target rules
-----------------------------------

The `goto_step` and `goto_job` actions allow you to jump the execution pointer to a different step within the same job (`goto_step`) or to a different job in the pipeline (`goto_job`). These actions can be used inside `conditions`, `when`, or as `else_action`.

Rules and behavior:

- `goto_step` requires a target step name in the same job. Use the `step` field with `conditions`/`when` or `else_step` when used with `else_action`.
- `goto_job` requires a target job name. Use the `job` field with `conditions`/`when` or `else_job` when used with `else_action`.
- If the specified target step or job is not found, `pipejob` will abort the run with an error (the tool currently returns exit code 6 in this case).
- If you use a `pipeline.runs` ordering list, `goto_job` will resolve job names against the effective execution order (it must exist in that list or among declared jobs).
- Step names are resolved only within their job; they do not need to be globally unique across jobs.

Additional goto_job behavior:

 - If a `goto_job` names a job that is not present in the effective execution order (`pipeline.runs`), `pipejob` will look for that job in the declared `jobs:` list. If found, the runner will insert the target job immediately after the current job and transfer execution there. After the inserted job finishes, execution continues with the remaining jobs from the original `runs` order. Note: this insertion may cause the same job to run twice if it also appears later in `pipeline.runs`.

Additional resume behavior:

 - When a `goto_job` is triggered from inside a job (for example from a `when` rule, legacy `conditions`, or as an `else_action`), `pipejob` will by default treat the jump as a temporary detour: it transfers execution to the target job immediately, and after the target job completes it resumes the remaining steps from the original job.
 - To implement this the runner inserts a one‑off "resume" job at runtime containing only the remaining steps from the original job. The resume job is executed exactly once and then discarded. The generated resume job name follows the pattern `<original>-resume-<timestamp>` (timestamp in nanoseconds) and will appear in logs for traceability.
 - If you do not want this detour-and-return behavior (i.e., you want a permanent transfer where the original job's remaining steps are skipped), let me know and we can add an explicit flag or alternate action (for example `goto_job.permanent: true` or a separate `call_job` action) to support that semantic.



Examples:

1) `goto_step` from a `when` rule:

```yaml
steps:
  - name: check
    type: command
    command: ./check.sh
    when:
      - contains: "SKIP_TO_DONE"
        action: goto_step
        step: done

  - name: done
    type: command
    command: echo "DONE"
```

2) `goto_job` example (works with or without `pipeline.runs`):

```yaml
pipeline:
  runs: [build, test, deploy]
jobs:
  - name: build
    steps:
      - name: maybe_skip
        type: command
        command: ./maybe.sh
        when:
          - contains: "SKIP_TESTS"
            action: goto_job
            job: deploy

  - name: test
    steps:
      - name: run_tests
        type: command
        command: ./run_tests.sh

  - name: deploy
    steps:
      - name: do_deploy
        type: command
        command: ./deploy.sh
```

3) `else_action` using `else_step` / `else_job`:

```yaml
steps:
  - name: check_or_recover
    type: command
    command: ./check.sh
    when:
      - contains: "OK"
        action: continue
    else_action: goto_step
    else_step: recover

  - name: recover
    type: command
    command: ./recover.sh
```

Implementation notes:

- The runner resolves step targets by name and sets the internal loop indices accordingly (so the next iteration begins at the requested step/job). If a target is missing you will see a clear error in stderr and the run will abort.
- Use `goto_job` sparingly: it changes the outer job execution pointer and can make control flow harder to follow. Prefer explicit `pipeline.runs` ordering and small, well-named steps.
