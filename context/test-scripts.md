# Test Scripts & Commands (Windows)

Below are suggested one-line commands and small scripts to exercise idle-timeout and taskkill behavior.

1) Basic idle timeout (PowerShell sleep)

```powershell
# YAML: examples/idle-timeout.yaml (adjust path)
# Run without build: ensure pipejob.exe is present
.\pipejob.exe examples\\idle-timeout.yaml
```

2) Global idle timeout

```powershell
.\pipejob.exe --idle-timeout 2s examples\\test.yaml
```

3) Simulate long-running child process

Create a PowerShell script that spawns a child that outlives the parent:

```powershell
# spawn-child.ps1
Start-Process -FilePath powershell -ArgumentList '-NoProfile','-Command','Start-Sleep -Seconds 300' -WindowStyle Hidden
Start-Sleep -Seconds 60
# exit (parent ends but child remains)
```

Use a step command that runs `powershell -File spawn-child.ps1` with `idle_timeout: 5s`. Expect `taskkill` to remove the spawned child when idle timeout triggers.

4) Verify taskkill fallback (permission denied)

- Run tests as normal user and attempt to kill a process started by SYSTEM or another user; ensure pipejob logs the failure and includes evidence in `.sync_temp`.

5) Capture output and verify exit code

```powershell
.\pipejob.exe examples\\idle-timeout.yaml; echo ExitCode:$LASTEXITCODE
```

Expect `ExitCode:0` if `on_timeout` handled via `goto_step`/`drop`, or `ExitCode:124` if raw timeout bubbled up and not handled.

6) Run under different shells

```powershell
.\pipejob.exe --shell powershell examples\\idle-timeout.yaml
.\pipejob.exe --shell cmd examples\\idle-timeout-cmd-format.yaml
```

Notes
-----
- Add `--persist-logs C:\temp\pipejob-logs` to keep logs during tests.
- If using CI, configure a Windows runner with elevated permissions for process-tree tests, and a normal runner to validate permission failure behavior.
