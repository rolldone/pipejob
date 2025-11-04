package main

import (
	"context"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// resolveJobIndex looks for `target` in the current execJobs slice. If not
// found, it searches the full list of declared jobs `allJobs`. If the target
// exists in `allJobs` but not in `execJobs`, it inserts the job immediately
// after the given `insertAfter` index so execution will return to the
// original sequence afterwards. Returns (index, true) if found,
// (-1, false) otherwise.
func resolveJobIndexExec(execJobs *[]Job, allJobs []Job, target string, insertAfter int) (int, bool) {
	for i := range *execJobs {
		if (*execJobs)[i].Name == target {
			return i, true
		}
	}
	for _, j := range allJobs {
		if j.Name == target {
			// insert j after insertAfter (clamp bounds)
			pos := insertAfter + 1
			if pos < 0 {
				pos = 0
			}
			if pos > len(*execJobs) {
				pos = len(*execJobs)
			}
			// perform insert
			*execJobs = append((*execJobs)[:pos], append([]Job{j}, (*execJobs)[pos:]...)...)
			return pos, true
		}
	}
	return -1, false
}

// runLocalCommand runs the given command line via a shell and returns the
// process exit code and an error (if any). It supports a total `timeout`
// and an `idleTimeout` which cancels the command if no stdout/stderr
// activity is observed for the duration. On timeout the function returns
// exit code 124.
func runLocalCommandExec(cmdLine string, timeout time.Duration, idleTimeout time.Duration, stdout io.Writer, stderr io.Writer) (int, error) {
	var cmd *exec.Cmd
	sh := runtimeShell
	if sh == "" {
		if runtime.GOOS == "windows" {
			sh = "cmd"
		} else {
			sh = "sh"
		}
	}

	// create a cancellable context for the command (total timeout support)
	cmdCtx, cancel := context.WithCancel(context.Background())
	if timeout > 0 {
		var toCancel context.CancelFunc
		cmdCtx, toCancel = context.WithTimeout(context.Background(), timeout)
		// wrap cancel so we call both
		prevCancel := cancel
		cancel = func() {
			toCancel()
			prevCancel()
		}
	}
	defer cancel()

	// build command using context so exec kills on ctx cancel where supported
	switch strings.ToLower(sh) {
	case "cmd":
		cmd = exec.CommandContext(cmdCtx, "cmd", "/C", cmdLine)
	case "powershell":
		cmd = exec.CommandContext(cmdCtx, "powershell", "-NoProfile", "-Command", cmdLine)
	default:
		cmd = exec.CommandContext(cmdCtx, "/bin/sh", "-lc", cmdLine)
	}

	// ensure children are placed in their own process group on Unix so we
	// can kill the entire group on timeout.
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	// use pipes so we can observe stdout/stderr activity for the idle timer
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return 1, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return 1, err
	}

	if err := cmd.Start(); err != nil {
		return 1, err
	}

	activity := make(chan struct{}, 1)
	// helper to copy and notify activity
	copyNotify := func(r io.ReadCloser, w io.Writer) {
		defer r.Close()
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				// write to provided writer (this may be a bytes.Buffer)
				w.Write(buf[:n])
				// notify activity (non-blocking)
				select {
				case activity <- struct{}{}:
				default:
				}
			}
			if err != nil {
				if err == io.EOF {
					return
				}
				return
			}
		}
	}

	go copyNotify(stdoutPipe, stdout)
	go copyNotify(stderrPipe, stderr)

	// monitor idle timeout, ctx.Done, and cmd completion
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	timedOut := false
	var waitErr error
	if idleTimeout > 0 {
		idleTimer := time.NewTimer(idleTimeout)
		defer idleTimer.Stop()
		for {
			select {
			case <-activity:
				if !idleTimer.Stop() {
					select {
					case <-idleTimer.C:
					default:
					}
				}
				idleTimer.Reset(idleTimeout)
			case <-idleTimer.C:
				// idle timeout fired
				timedOut = true
				cancel()
				// attempt to kill process group (Unix) or process (Windows)
				if cmd.Process != nil {
					if runtime.GOOS != "windows" {
						// negative pid indicates pgid
						_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					} else {
						// On Windows try to kill the whole process tree using taskkill
						if cmd.Process != nil {
							_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
						}
					}
				}
				// wait for command to exit
				waitErr = <-done
				goto AFTER_WAIT
			case <-cmdCtx.Done():
				// total timeout or cancel
				waitErr = <-done
				goto AFTER_WAIT
			case err := <-done:
				waitErr = err
				goto AFTER_WAIT
			}
		}
	} else {
		// no idle timer: just wait for completion or ctx.Done
		select {
		case <-cmdCtx.Done():
			// canceled/timeout
			timedOut = cmdCtx.Err() == context.DeadlineExceeded
			if cmd.Process != nil {
				if runtime.GOOS != "windows" {
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				} else {
					_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
				}
			}
			waitErr = <-done
		case err := <-done:
			waitErr = err
		}
	}

AFTER_WAIT:
	if waitErr == nil {
		return 0, nil
	}
	// treat ctx deadline exceeded or our timedOut as exit code 124
	if timedOut || (cmdCtx.Err() == context.DeadlineExceeded) {
		return 124, waitErr
	}
	// try to extract exit code from *exec.ExitError
	if ee, ok := waitErr.(*exec.ExitError); ok {
		if status, ok2 := ee.Sys().(interface{ ExitStatus() int }); ok2 {
			return status.ExitStatus(), waitErr
		}
	}
	return 1, waitErr
}
