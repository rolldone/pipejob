# Windows Test Plan — pipejob idle_timeout & process-tree termination

Goal
----
Verify that `pipejob` correctly enforces `idle_timeout`, kills long-idle processes, and attempts to terminate the full process tree on Windows (via `taskkill /T /F <PID>`), and that related flags behave as expected.

Scope
-----
- Per-step `idle_timeout` (explicit in YAML)
- Global `--idle-timeout` defaulting behavior
- `on_timeout` handling (continue, drop, goto_step, goto_job, fail)
- Windows-specific termination via `taskkill` and failure modes (permission, missing taskkill)
- Logging, `--persist-logs`, and in-memory buffer flush on error
- Shell variations: `cmd` vs `powershell` behavior

High-level test cases
---------------------
1. Basic idle timeout
   - YAML: step runs `sleep` equivalent (PowerShell `Start-Sleep`) with `idle_timeout: 2s` and no output
   - Expect: process killed after ~2s, step treated as timeout (exit code 124), `on_timeout` rules apply

2. Global default idle timeout
   - Run `pipejob --idle-timeout 2s` on a YAML with no per-step `idle_timeout`
   - Expect: idle timeout fires as above

3. Step-level override precedence
   - Global `--idle-timeout 5s`, step `idle_timeout: 1s`
   - Expect: step idle timeout uses 1s (step-level wins)

4. Windows process-tree termination
   - Step spawns a child process that keeps running when parent killed (simulate background child)
   - Expect: `taskkill /T /F <PID>` attempts to kill both parent and child; verify child not left running
   - Test variations: require admin vs non-admin privileges

5. taskkill absent or restricted
   - Simulate environment where `taskkill` is not available or permission denied
   - Expect: runner logs clear error/evidence and behavior falls back to best-effort kill (Process.Kill)

6. Shell differences
   - Run same YAML under `--shell cmd`, `--shell powershell`
   - Expect: consistent idle-timeout enforcement; command format differences handled

7. on_timeout actions
   - Verify `on_timeout: goto_step` and `on_timeout: goto_job` trigger expected flow

8. Logging & persist
   - Confirm that when a timeout occurs and run exits non-zero, in-memory buffer is flushed to `.sync_temp/.../run.log` unless `--persist-logs` was used (then logs were streamed live)

9. Exit codes
   - Confirm timeout returns exit code 124 and `fail`/`drop` actions result in expected exit codes

Acceptance criteria
-------------------
- All test cases must include steps to reproduce, observed behavior, and expected behavior.
- Any divergence must be captured in `bug-report-template.md` with logs attached.

