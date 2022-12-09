package cmd_util

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	// "log"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"gitee.com/wangning057/ninja_server/status"
	ps "github.com/mitchellh/go-ps"
)

const (
	// KilledExitCode is a special exit code value used by the "os/exec" package
	// when a process is killed.
	KilledExitCode = -1
	// NoExitCode indicates a missing exit code value, usually because the process
	// never started, or its actual exit code could not be determined because of an
	// error.
	NoExitCode = -2
)

type Command struct {
	Content     string
	Env         []string // Each entry is of the form "key=value".
	Use_console bool
}

type Stdio struct {
	// Stdin is an optional stdin source for the executed process.
	Stdin io.Reader
	// Stdout is an optional stdout sink for the executed process.
	Stdout io.Writer
	// Stderr is an optional stderr sink for the executed process.
	Stderr io.Writer
}

type CommandResult struct {
	// 只有在命令无法启动，或者已经启动但没有完成的情况下，才会弹出错误信息。
	// 启动了但没有完成。
	//
	// 特别是，如果该命令运行并返回一个非零的退出代码（比如1）。
	// 这被认为是一个成功的执行，这个错误将不会被填入。
	//
	// 在某些情况下，该命令可能由于与命令本身无关的问题而未能启动。
	// 与命令本身无关。例如，运行器可能在一个
	// 但未能创建沙盒。在这种情况下，这里的
	// 这里的Error字段应填入一个gRPC错误代码，说明为什么该
	// 而ExitCode字段应该包含沙盒进程的退出代码。
	// 而ExitCode字段应该包含来自沙盒进程的退出代码，而不是命令本身。
	//
	// 如果对 "exec.Cmd#Run "的调用返回-1，意味着该命令被杀死或者
	// 如果对`exec.Cmd#Run'的调用返回-1，意味着命令被杀死或从未退出，那么这个字段应填入一个gRPC错误代码，说明其
	// 这个字段应该填入一个gRPC错误代码，说明原因，比如DEADLINE_EXCEEDED（如果命令超时），UNAVAILABLE（如果
	// 有一个可以重试的瞬时错误），或RESOURCE_EXHAUSTED（如果该
	// 命令在执行过程中耗尽了内存）。
	Error error
	// CommandDebugString表示运行的命令，仅用于调试目的。
	// CommandDebugString string

	// Stdout from the command. This may contain data even if there was an Error.
	Stdout []byte
	// Stderr from the command. This may contain data even if there was an Error.
	Stderr []byte

	// ExitCode is one of the following:
	// * The exit code returned by the executed command
	// * -1 if the process was killed or did not exit
	// * -2 (NoExitCode) if the exit code could not be determined because it returned
	//   an error other than exec.ExitError. This case typically means it failed to start.
	ExitCode int

	// UsageStats持有该命令的测量资源使用量。如果该命令的隔离类型没有实现资源测量，它可能为零。
	// UsageStats *repb.UsageStats
}

func constructExecCommand(command *Command, workDir string, stdio *Stdio) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer, error) {
	if stdio == nil {
		stdio = &Stdio{}
	}
	// Note: we don't use CommandContext here because the default behavior of
	// CommandContext is to kill just the top-level process when the context is
	// canceled. Instead, we would rather kill the entire process group to ensure
	// that child processes are killed too.
	//cmd := exec.Command(executable, args...)
	var cmd *exec.Cmd
	executable := "/bin/bash"

	cmd = exec.Command(executable, "-c", command.Content)
	// cmd = exec.Command(command.Content)

	// fmt.Printf("cmd:%v\n", cmd)
	if workDir != "" {
		cmd.Dir = workDir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	if stdio.Stdout != nil {
		cmd.Stdout = stdio.Stdout
	}
	cmd.Stderr = &stderr
	if stdio.Stderr != nil {
		cmd.Stderr = stdio.Stderr
	}
	// Note: We are using StdinPipe() instead of cmd.Stdin here, because the
	// latter approach results in a bug where cmd.Wait() can hang indefinitely if
	// the process doesn't consume its stdin. See
	// https://go.dev/play/p/DpKaVrx8d8G
	if stdio.Stdin != nil {
		inp, err := cmd.StdinPipe()
		if err != nil {
			return nil, nil, nil, errors.New(fmt.Sprintf("failed to get stdin pipe: %s", err))
		}
		go func() {
			defer inp.Close()
			io.Copy(inp, stdio.Stdin)
		}()
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	for _, envVar := range command.Env {
		cmd.Env = append(cmd.Env, envVar)
	}
	return cmd, &stdout, &stderr, nil
}

func RetryIfTextFileBusy(fn func() error) error {
	nBusy := 0
	for {
		err := fn()
		if err != nil && nBusy < 3 && strings.Contains(err.Error(), "text file busy") {
			nBusy++
			time.Sleep(100 * time.Millisecond << uint(nBusy))
			continue
		}
		return err
	}
}

func Run(ctx context.Context, command *Command, workDir string, stdio *Stdio) *CommandResult {
	var cmd *exec.Cmd
	var stdoutBuf, stderrBuf *bytes.Buffer

	err := RetryIfTextFileBusy(func() error {
		// Create a new command on each attempt since commands can only be run once.
		var err error
		cmd, stdoutBuf, stderrBuf, err = constructExecCommand(command, workDir, stdio)
		if err != nil {
			return err
		}
		err = RunWithProcessTreeCleanup(ctx, cmd)
		return err
	})

	if err != nil {
		fmt.Printf("err:\033[1;38;40m%v\033[0m\n", err)
	}

	exitCode, err := ExitCode(ctx, cmd, err)

	if stdoutBuf.Len() > 0 {
		fmt.Printf("cmd:%v,stdout:\033[1;38;20m%v\033[0m\n", cmd, stdoutBuf)
	}
	if stderrBuf.Len() > 0 {
		fmt.Printf("cmd:%v,stderr:\033[1;31;40m%v\033[0m\n", cmd, stderrBuf)
	}

	return &CommandResult{
		ExitCode: exitCode,
		Error:    err,
		Stdout:   stdoutBuf.Bytes(),
		Stderr:   stderrBuf.Bytes(),
	}
}

func ExitCode(ctx context.Context, cmd *exec.Cmd, err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	// exec.Error is only returned when `exec.LookPath` fails to classify a file as an executable.
	// This could be a "not found" error or a permissions error, but we just report it as "not found".
	//
	// See:
	// - https://golang.org/pkg/os/exec/#Error
	// - https://github.com/golang/go/blob/fcb9d6b5d0ba6f5606c2b5dfc09f75e2dc5fc1e5/src/os/exec/lp_unix.go#L35
	if notFoundErr, ok := err.(*exec.Error); ok {
		return NoExitCode, status.NotFoundError(notFoundErr.Error())
	}

	// If we fail to get the exit code of the process for any other reason, it might
	// be a transient error that the client can retry, so return UNAVAILABLE for now.
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return NoExitCode, status.UnavailableError(err.Error())
	}
	processState := exitErr.ProcessState
	if processState == nil {
		return NoExitCode, status.UnavailableError(err.Error())
	}

	exitCode := processState.ExitCode()

	// TODO(bduffany): Extract syscall.WaitStatus from exitErr.Sys(), and set
	// ErrSIGKILL if waitStatus.Signal() == syscall.SIGKILL, so that the command
	// can be retried if it was OOM killed. Note that KilledExitCode does not
	// imply that SIGKILL was received.

	if exitCode == KilledExitCode {
		if dl, ok := ctx.Deadline(); ok && time.Now().After(dl) {
			return exitCode, status.DeadlineExceededErrorf("Command timed out: %s", err.Error())
		}
		// If the command didn't time out, it was probably killed by the kernel due to OOM.
		return exitCode, status.ResourceExhaustedErrorf("Command was killed: %s", err.Error())
	}

	return exitCode, nil
}

func RunWithProcessTreeCleanup(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	// pid := cmd.Process.Pid
	// processTerminated := make(chan struct{})
	// Cleanup goroutine: kill the process tree when the context is canceled.
	// go func() {
	// 	select {
	// 	case <-processTerminated:
	// 		return
	// 	case <-ctx.Done():
	// 		if err := KillProcessTree(pid); err != nil {
	// 			log.Printf("Failed to kill process tree: %s", err)
	// 		}
	// 	}
	// }()
	
	err := cmd.Wait()
	if err != nil {
		fmt.Println("cmd.Wait()出错：", err)
	}
	return err
}

func KillProcessTree(pid int) error {
	var lastErr error

	// Run a BFS on the process tree to build up a list of processes to kill.
	// Before listing child processes for each pid, send SIGSTOP to prevent it
	// from spawning new child processes. Otherwise the child process list has a
	// chance to become stale if the pid forks a new child just after we list
	// processes but before we send SIGKILL.

	pidsToExplore := []int{pid}
	pidsToKill := []int{}
	for len(pidsToExplore) > 0 {
		pid := pidsToExplore[0]
		pidsToExplore = pidsToExplore[1:]
		if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
			lastErr = err
			// If we fail to SIGSTOP, proceed anyway; the more we can clean up,
			// the better.
		}
		pidsToKill = append(pidsToKill, pid)

		childPids, err := ChildPids(pid)
		if err != nil {
			lastErr = err
			continue
		}
		pidsToExplore = append(pidsToExplore, childPids...)
	}
	for _, pid := range pidsToKill {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			lastErr = err
		}
	}

	return lastErr
}

func ChildPids(pid int) ([]int, error) {
	procs, err := ps.Processes()
	if err != nil {
		return nil, err
	}
	var out []int
	for _, proc := range procs {
		if proc.PPid() != pid {
			continue
		}
		out = append(out, proc.Pid())
	}
	return out, nil
}
