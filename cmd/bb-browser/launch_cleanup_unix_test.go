//go:build unix

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	switch os.Getenv("BB_BROWSER_LAUNCH_HELPER") {
	case "sleep", "listen":
		runLaunchHelper(os.Getenv("BB_BROWSER_LAUNCH_HELPER"))
		os.Exit(0)
	default:
		os.Exit(m.Run())
	}
}

func runLaunchHelper(mode string) {
	if pidfile := os.Getenv("BB_BROWSER_LAUNCH_PIDFILE"); pidfile != "" {
		if err := os.WriteFile(pidfile, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	if mode == "listen" {
		port := ""
		for _, arg := range os.Args[1:] {
			const prefix = "--remote-debugging-port="
			if strings.HasPrefix(arg, prefix) {
				port = strings.TrimPrefix(arg, prefix)
			}
		}
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		defer ln.Close()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_ = c.Close()
			}
		}()
	}
	time.Sleep(30 * time.Second)
}

func TestLaunchWaitTimeoutStopsProcess(t *testing.T) {
	pid, _, err := runFakeLaunch(t, "sleep", 300*time.Millisecond, false)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error = %v, want CDP timeout", err)
	}
	if processRunning(pid) {
		t.Fatalf("pid %d still running after wait timeout", pid)
	}
}

func TestLaunchSIGINTStopsProcess(t *testing.T) {
	pid, _, err := runFakeLaunch(t, "sleep", 15*time.Second, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if processRunning(pid) {
		t.Fatalf("pid %d still running after SIGINT", pid)
	}
}

func TestLaunchReadyDetaches(t *testing.T) {
	pid, port, err := runFakeLaunch(t, "listen", 10*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	if !processRunning(pid) {
		t.Fatalf("pid %d was stopped on the success path", pid)
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatalf("detached process is not listening: %v", err)
	}
	_ = conn.Close()
}

func runFakeLaunch(t *testing.T, helper string, timeout time.Duration, sigint bool) (int, int, error) {
	t.Helper()
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "pid")
	port := freePort(t)
	t.Setenv("BB_BROWSER_LAUNCH_HELPER", helper)
	t.Setenv("BB_BROWSER_LAUNCH_PIDFILE", pidfile)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	var pid int
	t.Cleanup(func() { stopPID(pid) })
	if sigint {
		go func() {
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				if readPID(pidfile) > 0 {
					_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
	}

	root := newRootCmd()
	root.SetArgs([]string{
		"launch",
		"--chrome", bin,
		"--profile", filepath.Join(dir, "profile"),
		"--port", strconv.Itoa(port),
		"--bind", "127.0.0.1",
		"--wait-timeout", timeout.String(),
	})
	err = withDiscardedStdio(root.Execute)
	pid = readPID(pidfile)
	if pid == 0 {
		t.Fatalf("chrome pid was not recorded (launch error: %v)", err)
	}
	return pid, port, err
}

func withDiscardedStdio(fn func() error) error {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer devnull.Close()
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devnull, devnull
	defer func() {
		os.Stdout, os.Stderr = stdout, stderr
	}()
	return fn()
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func readPID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return pid
}

func processRunning(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func stopPID(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	var ws syscall.WaitStatus
	_, _ = syscall.Wait4(pid, &ws, 0, nil)
}
