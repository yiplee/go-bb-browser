package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newLaunchCmd() *cobra.Command {
	var (
		chromePath string
		profile    string
		port       int
		bind       string
		headless   bool
		detach     bool
		extraArgs  []string
		startURL   string
		waitReady  bool
		readyTO    time.Duration
	)

	c := &cobra.Command{
		Use:   "launch [URL]",
		Short: "Launch Google Chrome with a CDP debugging endpoint and persistent profile",
		Long: strings.TrimSpace(`
Launch Google Chrome on this machine with --remote-debugging-port enabled, using a
persistent profile directory. Supports linux, windows, and macos. The daemon
(bb-daemon) can then attach to the printed debugger endpoint.

Examples:
  bb-browser launch
  bb-browser launch https://example.com
  bb-browser launch --port 9333 --profile /tmp/chrome-prof
  bb-browser launch --chrome /usr/bin/google-chrome
`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				startURL = strings.TrimSpace(args[0])
			}

			binary := strings.TrimSpace(chromePath)
			if binary == "" {
				binary = findChromeExecutable()
			}
			if binary == "" {
				return errors.New("could not find Google Chrome on this system; pass --chrome /path/to/chrome")
			}
			if _, err := exec.LookPath(binary); err != nil {
				if _, statErr := os.Stat(binary); statErr != nil {
					return fmt.Errorf("chrome binary not usable %q: %v", binary, err)
				}
			}

			profileDir := strings.TrimSpace(profile)
			if profileDir == "" {
				profileDir = defaultProfileDir()
			}
			profileDir, err := filepath.Abs(profileDir)
			if err != nil {
				return fmt.Errorf("resolve profile dir: %w", err)
			}
			if err := os.MkdirAll(profileDir, 0o755); err != nil {
				return fmt.Errorf("create profile dir %q: %w", profileDir, err)
			}

			if port <= 0 || port > 65535 {
				return fmt.Errorf("invalid --port %d", port)
			}
			bindAddr := strings.TrimSpace(bind)
			if bindAddr == "" {
				bindAddr = "127.0.0.1"
			}

			chromeArgs := buildChromeArgs(profileDir, bindAddr, port, headless, extraArgs, startURL)

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			var proc *exec.Cmd
			if detach {
				proc = exec.Command(binary, chromeArgs...)
				proc.Stdout = nil
				proc.Stderr = nil
			} else {
				proc = exec.CommandContext(ctx, binary, chromeArgs...)
				proc.Stdout = os.Stdout
				proc.Stderr = os.Stderr
			}
			configureDetach(proc, detach)

			if err := proc.Start(); err != nil {
				return fmt.Errorf("start chrome: %w", err)
			}

			endpoint := fmt.Sprintf("%s:%d", bindAddr, port)
			fmt.Fprintf(os.Stderr, "chrome: pid=%d binary=%s\n", proc.Process.Pid, binary)
			fmt.Fprintf(os.Stderr, "chrome: profile=%s\n", profileDir)
			fmt.Fprintf(os.Stderr, "chrome: debugger=%s (use --debugger-url with bb-daemon)\n", endpoint)

			if waitReady {
				if err := waitForCDP(ctx, bindAddr, port, readyTO); err != nil {
					return fmt.Errorf("waiting for CDP endpoint: %w", err)
				}
				fmt.Fprintln(os.Stderr, "chrome: CDP endpoint ready")
			}

			if detach {
				if err := releaseChild(proc); err != nil {
					fmt.Fprintf(os.Stderr, "warn: release child: %v\n", err)
				}
				fmt.Println(endpoint)
				return nil
			}

			fmt.Println(endpoint)
			if err := proc.Wait(); err != nil {
				return fmt.Errorf("chrome exited: %w", err)
			}
			return nil
		},
	}

	c.Flags().StringVar(&chromePath, "chrome", envOrDefault("BB_BROWSER_CHROME", ""), "path to Google Chrome binary (auto-detected when empty)")
	c.Flags().StringVarP(&profile, "profile", "p", envOrDefault("BB_BROWSER_PROFILE", ""), "user profile (--user-data-dir) directory")
	c.Flags().IntVar(&port, "port", 9222, "Chrome --remote-debugging-port")
	c.Flags().StringVar(&bind, "bind", "127.0.0.1", "Chrome --remote-debugging-address")
	c.Flags().BoolVar(&headless, "headless", false, "launch Chrome in --headless=new mode")
	c.Flags().BoolVar(&detach, "detach", true, "return immediately after Chrome starts (otherwise wait for it to exit)")
	c.Flags().StringSliceVar(&extraArgs, "extra-arg", nil, "extra argument(s) passed verbatim to Chrome (repeatable)")
	c.Flags().BoolVar(&waitReady, "wait-ready", true, "wait until the CDP endpoint accepts TCP connections before returning")
	c.Flags().DurationVar(&readyTO, "wait-timeout", 15*time.Second, "timeout for --wait-ready")
	return c
}

func buildChromeArgs(profileDir, bindAddr string, port int, headless bool, extra []string, startURL string) []string {
	args := []string{
		fmt.Sprintf("--user-data-dir=%s", profileDir),
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--remote-debugging-address=%s", bindAddr),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-features=ChromeWhatsNewUI",
	}
	if headless {
		args = append(args, "--headless=new")
	}
	args = append(args, extra...)
	if startURL != "" {
		args = append(args, startURL)
	}
	return args
}

func waitForCDP(ctx context.Context, host string, port int, timeout time.Duration) error {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %s probing %s", timeout, addr)
		}
		dialer := net.Dialer{Timeout: 500 * time.Millisecond}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func defaultProfileDir() string {
	const sub = "bb-browser-chrome-profile"
	if v := strings.TrimSpace(os.Getenv("BB_BROWSER_PROFILE")); v != "" {
		return v
	}
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = os.Getenv("APPDATA")
		}
		if base == "" {
			if h, err := os.UserHomeDir(); err == nil {
				base = filepath.Join(h, "AppData", "Local")
			}
		}
		return filepath.Join(base, "bb-browser", "chrome-profile")
	case "darwin":
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, "Library", "Application Support", "bb-browser", "chrome-profile")
		}
	}
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "bb-browser", "chrome-profile")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".cache", "bb-browser", "chrome-profile")
	}
	return filepath.Join(os.TempDir(), sub)
}

func findChromeExecutable() string {
	if v := strings.TrimSpace(os.Getenv("BB_BROWSER_CHROME")); v != "" {
		if p, err := exec.LookPath(v); err == nil {
			return p
		}
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}
	candidates := chromeCandidates()
	for _, name := range candidates {
		if strings.ContainsAny(name, `/\`) {
			if _, err := os.Stat(name); err == nil {
				return name
			}
			continue
		}
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

func chromeCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Google Chrome Beta.app/Contents/MacOS/Google Chrome Beta",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"google-chrome",
			"chromium",
		}
	case "windows":
		userProfile := os.Getenv("USERPROFILE")
		programFiles := os.Getenv("ProgramFiles")
		programFilesX86 := os.Getenv("ProgramFiles(x86)")
		if programFiles == "" {
			programFiles = `C:\Program Files`
		}
		if programFilesX86 == "" {
			programFilesX86 = `C:\Program Files (x86)`
		}
		paths := []string{
			filepath.Join(programFiles, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(programFilesX86, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(programFiles, `Google\Chrome Beta\Application\chrome.exe`),
			filepath.Join(programFiles, `Google\Chrome Dev\Application\chrome.exe`),
		}
		if userProfile != "" {
			paths = append(paths,
				filepath.Join(userProfile, `AppData\Local\Google\Chrome\Application\chrome.exe`),
				filepath.Join(userProfile, `AppData\Local\Google\Chrome SxS\Application\chrome.exe`),
				filepath.Join(userProfile, `AppData\Local\Chromium\Application\chrome.exe`),
			)
		}
		paths = append(paths, "chrome.exe", "chrome")
		return paths
	default:
		return []string{
			"google-chrome",
			"google-chrome-stable",
			"google-chrome-beta",
			"google-chrome-unstable",
			"chromium",
			"chromium-browser",
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/snap/bin/chromium",
			"/snap/bin/google-chrome",
			"/usr/local/bin/chrome",
		}
	}
}
