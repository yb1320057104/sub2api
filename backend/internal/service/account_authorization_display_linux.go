//go:build linux

package service

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const (
	authorizationDisplayReadyTimeout = 10 * time.Second
	authorizationDisplayStopTimeout  = 2 * time.Second
)

func authorizationBrowserDisplayEnv(ctx context.Context, mode string) ([]string, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, func() {}, err
	}
	if mode != authorizationModeBackground || os.Getenv("DISPLAY") != "" {
		return nil, func() {}, nil
	}
	binary, err := exec.LookPath("Xvfb")
	if err != nil {
		return nil, func() {}, authorizationDisplayFailure("missing")
	}
	// Xvfb atomically chooses a free display and writes its number to FD 3 only
	// after initialization. Existing displays and stale files are not ours to
	// delete; there is no application-managed :90..:99 slot pool.
	cmd := exec.Command(binary, "-displayfd", "3", "-screen", "0", "1366x900x24", "-nolisten", "tcp")
	return authorizationStartDisplay(ctx, cmd, authorizationDisplayReadyTimeout, authorizationDisplayStopTimeout)
}

func authorizationDisplayFailure(reason string) error {
	switch reason {
	case "missing":
		return authorizationBrowserFailure("browser_display_missing", "未找到可执行的 Xvfb，后台 Chrome 会话无法创建图形环境；请检查应用运行环境的 Xvfb 安装和 PATH，未提交账号密码")
	case "timeout":
		return authorizationBrowserFailure("browser_display_timeout", "Xvfb 图形环境未在时限内就绪，已停止本次启动；请检查系统资源与运行依赖，未提交账号密码")
	default:
		return authorizationBrowserFailure("browser_display_start_failed", "Xvfb 图形环境启动失败或未提供有效就绪通知；请检查 Xvfb 版本、运行权限与系统依赖，未提交账号密码")
	}
}

// Only this function owns the process and its Wait. Startup timeout bounds the
// ready handshake, while the parent context owns the entire display lifetime.
func authorizationStartDisplay(ctx context.Context, cmd *exec.Cmd, readyTimeout, stopTimeout time.Duration) ([]string, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, func() {}, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, func() {}, authorizationDisplayFailure("start")
	}
	defer reader.Close()
	cmd.ExtraFiles = []*os.File{writer}
	// nil maps to /dev/null without exec copy goroutines whose pipes could
	// otherwise delay Wait if an unexpected descendant inherited an output FD.
	cmd.Stdout, cmd.Stderr = nil, nil
	// Keep the PID 1 reaper away from this child from Start through Wait, even
	// when it exits before Chrome starts or this helper is called independently.
	authorizationChildStart()
	if err = cmd.Start(); err != nil {
		authorizationChildDone()
		_ = writer.Close()
		return nil, func() {}, authorizationDisplayFailure("start")
	}
	_ = writer.Close()
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		authorizationChildDone()
		close(done)
	}()
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			_ = reader.Close()
			select {
			case <-done:
				return
			default:
			}
			// Give Xvfb the chance to remove its own lock/socket. SIGKILL is
			// reserved for a child that does not exit within the grace period.
			_ = cmd.Process.Signal(syscall.SIGTERM)
			timer := time.NewTimer(stopTimeout)
			defer timer.Stop()
			select {
			case <-done:
				return
			case <-timer.C:
				_ = cmd.Process.Kill()
				<-done
			}
		})
	}
	stopOnCancel := context.AfterFunc(ctx, stop)
	cleanup := func() { stopOnCancel(); stop() }
	fail := func(err error) ([]string, func(), error) {
		cleanup()
		return nil, func() {}, err
	}
	ready := make(chan string, 1)
	go func() {
		// A malformed/missing newline is a failed handshake, never readiness.
		line, readErr := bufio.NewReader(io.LimitReader(reader, 32)).ReadString('\n')
		if readErr != nil {
			ready <- ""
			return
		}
		ready <- line
	}()
	timer := time.NewTimer(readyTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fail(ctx.Err())
	case <-timer.C:
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		return fail(authorizationDisplayFailure("timeout"))
	case <-done:
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		return fail(authorizationDisplayFailure("start"))
	case line := <-ready:
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		display, ok := authorizationDisplayNumber(line)
		if !ok {
			return fail(authorizationDisplayFailure("start"))
		}
		select {
		case <-done:
			return fail(authorizationDisplayFailure("start"))
		default:
		}
		return []string{"DISPLAY=:" + display}, cleanup, nil
	}
}

func authorizationDisplayNumber(line string) (string, bool) {
	if len(line) < 2 || len(line) > 6 || line[len(line)-1] != '\n' {
		return "", false
	}
	value := line[:len(line)-1]
	for _, char := range value {
		if char < '0' || char > '9' {
			return "", false
		}
	}
	number, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return "", false
	}
	return strconv.FormatUint(number, 10), true
}
