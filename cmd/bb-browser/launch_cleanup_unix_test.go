//go:build unix

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	switch os.Getenv("BB_BROWSER_LAUNCH_HELPER") {
	case "sleep", "listen", "hold":
		runLaunchHelper(os.Getenv("BB_BROWSER_LAUNCH_HELPER"))
		os.Exit(0)
	default:
		os.Exit(m.Run())
	}
}

func runLaunchHelper(mode string) {
	if mode == "hold" {
		// Stay up across SIGINT so the foreground test can see a context-cancel SIGKILL.
		signal.Ignore(os.Interrupt, syscall.SIGTERM)
	}
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

func TestLaunchForegroundSIGINTDoesNotKill(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "pid")
	port := freePort(t)
	t.Setenv("BB_BROWSER_LAUNCH_HELPER", "hold")
	t.Setenv("BB_BROWSER_LAUNCH_PIDFILE", pidfile)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	// Catch SIGINT here so a foreground launch, which does not install its own
	// handler, does not take down the test process.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT)
	defer signal.Stop(sigc)

	errc := make(chan error, 1)
	go func() {
		root := newRootCmd()
		root.SetArgs([]string{
			"launch",
			"--detach=false",
			"--chrome", bin,
			"--profile", filepath.Join(dir, "profile"),
			"--port", strconv.Itoa(port),
			"--bind", "127.0.0.1",
			"--wait-timeout", "2s",
		})
		errc <- withDiscardedStdio(root.Execute)
	}()
	received := false
	defer func() {
		if received {
			return
		}
		select {
		case <-errc:
		case <-time.After(8 * time.Second):
		}
	}()

	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for pid == 0 && time.Now().Before(deadline) {
		pid = readPID(pidfile)
		if pid == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	t.Cleanup(func() { stopPID(pid) })
	if pid == 0 {
		t.Fatal("chrome pid was not recorded")
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sigc:
	case <-time.After(2 * time.Second):
		t.Fatal("SIGINT was not delivered")
	}
	aliveUntil := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(aliveUntil) {
		if processKilled(pid) {
			t.Fatalf("pid %d was killed by SIGINT context cancellation", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}

	var launchErr error
	select {
	case launchErr = <-errc:
		received = true
	case <-time.After(8 * time.Second):
		t.Fatal("launch did not return")
	}
	if launchErr == nil || errors.Is(launchErr, context.Canceled) || !strings.Contains(launchErr.Error(), "timeout") {
		t.Fatalf("foreground launch error = %v, want CDP timeout", launchErr)
	}
	// stopStartedProcess Wait reaps the pid. Foreground cancellation only
	// SIGKILLs via CommandContext and leaves a zombie, which still exists.
	if !processRunning(pid) {
		t.Fatalf("pid %d was reaped; foreground wait failure must not Kill+Wait", pid)
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

// processKilled reports whether pid has exited. A zombie counts as killed;
// a running process does not. Wait4 reaps a zombie when it finds one.
func processKilled(pid int) bool {
	if !processRunning(pid) {
		return true
	}
	var ws syscall.WaitStatus
	wpid, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
	return err == nil && wpid == pid
}

func stopPID(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	var ws syscall.WaitStatus
	_, _ = syscall.Wait4(pid, &ws, 0, nil)
}
