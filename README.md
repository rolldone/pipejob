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
