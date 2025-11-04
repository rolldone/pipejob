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
