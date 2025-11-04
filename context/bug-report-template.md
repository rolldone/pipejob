# Bug Report Template — pipejob Windows tests

Use this template to capture any failures discovered during Windows testing.

- Title: short summary
- Test case (from `test-plan.md`):
- Date/time:
- Environment:
  - Windows version:
  - User account (admin / standard):
  - pipejob binary path and SHA (if built):
  - Go version (if built locally):
- Command run (exact):

Steps to reproduce
------------------
1. 
2. 
3. 

Observed behavior
-----------------
(attach stdout/stderr excerpts and timestamps)

Expected behavior
-----------------

Artifacts
---------
- `.sync_temp/pipejob-<timestamp>/run.log` (attach or paste relevant sections)
- Any additional logs (persist dir if `--persist-logs` used)
- Screenshot (optional)

Notes & hypothesis
------------------
(why this might be happening, e.g., permission issues, antivirus interference)

Priority / severity
-------------------
- Blocker / High / Medium / Low

Assigned to:

