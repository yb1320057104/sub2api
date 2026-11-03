//go:build linux

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const authorizationDisplayTestChild = "SUB2API_AUTH_DISPLAY_TEST_CHILD"

// The same test executable acts as a local display process; no shell, browser,
// credentials, proxy or network is involved in these lifecycle tests.
func TestAuthorizationBrowserDisplayHelperProcess(t *testing.T) {
	mode := os.Getenv(authorizationDisplayTestChild)
	if mode == "" {
		return
	}
	state := os.Getenv("SUB2API_AUTH_DISPLAY_TEST_STATE")
	terminated := make(chan os.Signal, 1)
	if mode == "ignore_term" {
		signal.Ignore(syscall.SIGTERM)
	} else {
		signal.Notify(terminated, syscall.SIGTERM)
	}
	if err := os.WriteFile(state, []byte("started"), 0600); err != nil {
		os.Exit(21)
	}
	if mode == "exit" {
		os.Exit(23)
	}
	if mode != "no_ready" {
		pipe := os.NewFile(3, "display-ready")
		if pipe == nil {
			os.Exit(24)
		}
		if _, err := pipe.WriteString(os.Getenv("SUB2API_AUTH_DISPLAY_TEST_READY")); err != nil {
			os.Exit(25)
		}
		_ = pipe.Close()
	}
	if mode == "ignore_term" {
		for {
			time.Sleep(time.Hour)
		}
	}
	<-terminated
	_ = os.WriteFile(state, []byte("terminated"), 0600)
	os.Exit(0)
}

func authorizationDisplayTestCommand(t *testing.T, mode, ready string) (*exec.Cmd, string) {
	t.Helper()
	binary, err := os.Executable()
	require.NoError(t, err)
	state := filepath.Join(t.TempDir(), "process-state")
	cmd := exec.Command(binary, "-test.run=^TestAuthorizationBrowserDisplayHelperProcess$", "-test.timeout=15s")
	cmd.Env = append(os.Environ(), authorizationDisplayTestChild+"="+mode,
		"SUB2API_AUTH_DISPLAY_TEST_STATE="+state, "SUB2API_AUTH_DISPLAY_TEST_READY="+ready)
	return cmd, state
}

func authorizationDisplayTestState(t *testing.T, state, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(state)
		return err == nil && string(data) == want
	}, 5*time.Second, 10*time.Millisecond)
}

func authorizationDisplayTestFailure(t *testing.T, err error, stage string) {
	t.Helper()
	var failure *authorizationBrowserError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, stage, failure.stage)
	require.Contains(t, failure.message, "未提交账号密码")
	require.NotContains(t, failure.message, "fixture-private-path")
	require.False(t, authorizationMayChangeProxy(err), "display failures must not rotate the authorization proxy")
}

func TestAuthorizationBrowserDisplayMissingAndExistingDisplay(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("DISPLAY", "")
	env, stop, err := authorizationBrowserDisplayEnv(context.Background(), authorizationModeBackground)
	stop()
	require.Nil(t, env)
	authorizationDisplayTestFailure(t, err, "browser_display_missing")
	for _, mode := range []string{authorizationModeHeadless, authorizationModeBackground} {
		if mode == authorizationModeBackground {
			t.Setenv("DISPLAY", ":existing")
		}
		env, stop, err = authorizationBrowserDisplayEnv(context.Background(), mode)
		require.NoError(t, err)
		require.Nil(t, env)
		stop()
	}
}

func TestAuthorizationBrowserDisplayReadyAndGracefulStop(t *testing.T) {
	before := authorizationChildRunning.Load()
	cmd, state := authorizationDisplayTestCommand(t, "ready", "321\n")
	env, stop, err := authorizationStartDisplay(context.Background(), cmd, 5*time.Second, time.Second)
	require.NoError(t, err)
	t.Cleanup(stop)
	require.Equal(t, []string{"DISPLAY=:321"}, env)
	require.NoError(t, cmd.Process.Signal(syscall.Signal(0)), "successful return must leave the display alive")
	require.Equal(t, before+1, authorizationChildRunning.Load(), "reaper guard covers the live display")
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); stop() }()
	}
	wg.Wait()
	authorizationDisplayTestState(t, state, "terminated")
	require.NotNil(t, cmd.ProcessState)
	require.True(t, cmd.ProcessState.Success(), "normal cleanup must allow SIGTERM handling")
	require.Equal(t, before, authorizationChildRunning.Load())
}

func TestAuthorizationBrowserDisplayStartFailures(t *testing.T) {
	t.Run("exec failure", func(t *testing.T) {
		before := authorizationChildRunning.Load()
		cmd := exec.Command(filepath.Join(t.TempDir(), "fixture-private-path"))
		env, stop, err := authorizationStartDisplay(context.Background(), cmd, time.Second, time.Second)
		stop()
		require.Nil(t, env)
		authorizationDisplayTestFailure(t, err, "browser_display_start_failed")
		require.Equal(t, before, authorizationChildRunning.Load())
	})
	for _, tc := range []struct{ name, mode, ready string }{
		{"early exit", "exit", ""},
		{"empty handshake", "ready", ""},
		{"missing newline", "ready", "123"},
		{"negative display", "ready", "-1\n"},
		{"unexpected text", "ready", "fixture-private-path\n"},
		{"out of range", "ready", "65536\n"},
		{"oversized handshake", "ready", strings.Repeat("1", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := authorizationChildRunning.Load()
			cmd, _ := authorizationDisplayTestCommand(t, tc.mode, tc.ready)
			env, stop, err := authorizationStartDisplay(context.Background(), cmd, 5*time.Second, time.Second)
			stop()
			require.Nil(t, env)
			authorizationDisplayTestFailure(t, err, "browser_display_start_failed")
			require.NotNil(t, cmd.ProcessState, "failure must join its child")
			require.Equal(t, before, authorizationChildRunning.Load())
		})
	}
}

func TestAuthorizationBrowserDisplayStartupTimeout(t *testing.T) {
	cmd, _ := authorizationDisplayTestCommand(t, "no_ready", "")
	env, stop, err := authorizationStartDisplay(context.Background(), cmd, 300*time.Millisecond, time.Second)
	stop()
	require.Nil(t, env)
	authorizationDisplayTestFailure(t, err, "browser_display_timeout")
	require.NotNil(t, cmd.ProcessState)
}

func TestAuthorizationBrowserDisplayCancellation(t *testing.T) {
	t.Run("before start", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		cmd, _ := authorizationDisplayTestCommand(t, "ready", "123\n")
		_, stop, err := authorizationStartDisplay(ctx, cmd, time.Second, time.Second)
		stop()
		require.ErrorIs(t, err, context.Canceled)
		require.Nil(t, cmd.Process)
	})
	t.Run("waiting for ready", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cmd, state := authorizationDisplayTestCommand(t, "no_ready", "")
		result := make(chan error, 1)
		go func() {
			_, stop, err := authorizationStartDisplay(ctx, cmd, 10*time.Second, time.Second)
			stop()
			result <- err
		}()
		authorizationDisplayTestState(t, state, "started")
		cancel()
		select {
		case err := <-result:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(3 * time.Second):
			t.Fatal("display startup did not stop after cancellation")
		}
		require.NotNil(t, cmd.ProcessState)
		authorizationDisplayTestState(t, state, "terminated")
	})
	t.Run("after ready", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cmd, state := authorizationDisplayTestCommand(t, "ready", "123\n")
		_, stop, err := authorizationStartDisplay(ctx, cmd, 5*time.Second, time.Second)
		require.NoError(t, err)
		t.Cleanup(stop)
		cancel()
		authorizationDisplayTestState(t, state, "terminated")
		stop()
		require.True(t, cmd.ProcessState.Success())
	})
}

func TestAuthorizationBrowserDisplayForcedStop(t *testing.T) {
	cmd, _ := authorizationDisplayTestCommand(t, "ignore_term", "123\n")
	_, stop, err := authorizationStartDisplay(context.Background(), cmd, 5*time.Second, 80*time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(stop)
	stop()
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	require.True(t, status.Signaled())
	require.Equal(t, syscall.SIGKILL, status.Signal())
}

// Explicitly enabled only in a disposable Linux environment with Xvfb and a
// private writable /tmp. It launches no Chromium and sends no network request.
func TestAuthorizationBrowserDisplayXvfbIntegration(t *testing.T) {
	if os.Getenv("PK_AUTH_DISPLAY_INTEGRATION") != "1" {
		t.Skip("requires an explicitly isolated Xvfb environment")
	}
	t.Setenv("DISPLAY", "")
	_, err := exec.LookPath("Xvfb")
	require.NoError(t, err)
	// Capture any pre-existing fixture locks. Neither allocation nor cleanup
	// may remove or change these files; the fixture can preseed all ten old slots.
	existing := make(map[string][]byte)
	for number := 90; number < 100; number++ {
		path := fmt.Sprintf("/tmp/.X%d-lock", number)
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			existing[path] = data
		} else {
			require.True(t, errors.Is(readErr, os.ErrNotExist))
		}
	}
	type result struct {
		env  []string
		stop func()
		err  error
	}
	results := make(chan result, 12)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for range cap(results) {
		go func() {
			env, stop, err := authorizationBrowserDisplayEnv(ctx, authorizationModeBackground)
			results <- result{env, stop, err}
		}()
	}
	started := make([]result, 0, cap(results))
	for range cap(results) {
		entry := <-results
		started = append(started, entry)
		t.Cleanup(entry.stop)
	}
	displays := make(map[string]bool)
	for _, entry := range started {
		require.NoError(t, entry.err)
		require.Len(t, entry.env, 1)
		number := strings.TrimPrefix(entry.env[0], "DISPLAY=:")
		_, err := strconv.ParseUint(number, 10, 16)
		require.NoError(t, err)
		require.False(t, displays[number], "simultaneous sessions must own distinct displays")
		displays[number] = true
		_, err = os.Stat("/tmp/.X11-unix/X" + number)
		require.NoError(t, err, "displayfd must not report ready before the X socket exists")
	}
	for _, entry := range started {
		entry.stop()
		entry.stop()
	}
	for number := range displays {
		_, lockErr := os.Stat("/tmp/.X" + number + "-lock")
		_, socketErr := os.Stat("/tmp/.X11-unix/X" + number)
		require.ErrorIs(t, lockErr, os.ErrNotExist, "graceful shutdown must remove its own lock")
		require.ErrorIs(t, socketErr, os.ErrNotExist, "graceful shutdown must remove its own socket")
	}
	for path, before := range existing {
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, before, after, "pre-existing locks do not belong to this helper")
	}
}
