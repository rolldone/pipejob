# Test Environment — Windows runner for pipejob

Prerequisites
-------------
- A Windows machine or Windows runner (Windows Server 2016+ or Windows 10+ recommended)
- Go runtime (for building) OR use pre-built `pipejob` binary built on same architecture
- `taskkill` (standard on Windows) — note: may require Administrative privileges to kill some processes
- PowerShell available (for `Start-Sleep` and process spawning tests)
- Sufficient permissions to create and remove `.sync_temp` folders

Suggested setup steps
---------------------
1. Build (on Windows) or copy binary:

```powershell
# build (optional, requires Go)
cd C:\path\to\repo\sub_app\pipejob
go build -o pipejob.exe

# or copy prebuilt pipejob.exe into the folder
```

2. Ensure `PATH` includes `C:\Windows\System32` so `taskkill` is accessible.
3. Disable aggressive antivirus or whitelist test folder if anti-virus kills spawned child processes (may change test results).
4. Prepare test YAML files in `sub_app/pipejob/examples` (examples/idle-timeout.yaml exists).
5. Run tests from an elevated shell for privileged scenarios; also test from a normal user account to validate permission failures.

Logging & artifacts
-------------------
- Temporary logs are written to `.sync_temp/pipejob-<timestamp>/run.log` when a run fails.
- If using `--persist-logs DIR`, logs are streamed to the specified DIR.

Notes on privileges
-------------------
- `taskkill /T /F` may require admin for processes started by other users. When testing as a normal user, expect permission-denied cases to be captured as graceful fallback and logged.

