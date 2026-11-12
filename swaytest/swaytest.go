// Package swaytest runs a private headless sway for tests, so Wayland
// clients connect to it instead of the developer's session.
package swaytest

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/sockdir"
	"github.com/stubbedev/lys-lib/testhome"
)

const (
	logName = "log"
	pidName = "pid"
)

// Start runs a private headless sway rendering with pixman, env
// overriding its environment (WLR_RENDERER=vulkan, a render node), and
// points WAYLAND_DISPLAY and XDG_RUNTIME_DIR at it for the test. It gets
// a private session bus: without one, nixpkgs' wrapper runs it under
// dbus-run-session, whose process is not sway's. Its log is shown when
// it fails to start. A host without sway skips the test.
func Start(t *testing.T, env ...string) *os.Process {
	t.Helper()
	if _, err := exec.LookPath("sway"); err != nil {
		t.Skip("sway is not installed (the nix dev shell carries it)")
	}
	dir := sockdir.Dir(t)
	config := filepath.Join(dir, "config")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, logName)) //nolint:gosec // a file in the test's own directory
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command("sway", "-d", "-c", config) //nolint:gosec // sway from PATH, for a test
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(testhome.WithoutSession(os.Environ()), "XDG_RUNTIME_DIR="+dir, "DBUS_SESSION_BUS_ADDRESS="+dbustest.Start(t).Address, "WLR_BACKENDS=headless", "WLR_RENDERER=pixman", "WLR_LIBINPUT_NO_DEVICES=1")
	cmd.Env = append(cmd.Env, env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	w := watchSway(cmd.Process.Pid, "sway started")
	if err := os.WriteFile(filepath.Join(dir, pidName), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	for deadline := w.began.Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("sway never opened its Wayland socket in %s\n%s", dir, w.report(dir))
		}
		sockets, _ := filepath.Glob(filepath.Join(dir, "wayland-*"))
		sockets = slices.DeleteFunc(sockets, func(s string) bool { return strings.HasSuffix(s, ".lock") })
		if len(sockets) > 0 && listening(sockets[0]) {
			t.Setenv("XDG_RUNTIME_DIR", dir)
			t.Setenv("WAYLAND_DISPLAY", filepath.Base(sockets[0]))
			return cmd.Process
		}
	}
}

func listening(socket string) bool {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Msg runs swaymsg with args against the sway Start ran for this test
// (its IPC socket in XDG_RUNTIME_DIR), failing the test when sway
// refuses the command, and returns its output. A failure reports sway's
// process and thread states, the CPU time it got while swaymsg waited,
// the host's load, a backtrace when eu-stack or gdb is on PATH, and its
// log.
func Msg(t *testing.T, args ...string) []byte {
	t.Helper()
	out, err := msg(os.Getenv("XDG_RUNTIME_DIR"), args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func msg(dir string, args ...string) ([]byte, error) {
	pid, err := readPID(dir)
	if err != nil {
		return nil, err
	}
	socket := filepath.Join(dir, fmt.Sprintf("sway-ipc.%d.%d.sock", os.Getuid(), pid))
	w := watchSway(pid, "the wait for its IPC socket began")
	for deadline := w.began.Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		_, err := os.Lstat(socket) //nolint:gosec // a socket in the test's own directory
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("look for sway's IPC socket: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("sway never opened its IPC socket %s\n%s", socket, w.report(dir))
		}
	}
	w = watchSway(pid, "swaymsg started")
	out, err := exec.Command("swaymsg", append([]string{"-s", socket}, args...)...).CombinedOutput() //nolint:gosec // swaymsg from PATH, for a test
	if err != nil {
		return nil, fmt.Errorf("swaymsg %s: %w after %v: %s\n%s", strings.Join(args, " "), err, time.Since(w.began), out, w.report(dir))
	}
	return out, nil
}

func readPID(dir string) (int, error) {
	name := filepath.Join(dir, pidName)
	b, err := os.ReadFile(name) //nolint:gosec // a file in the test's own directory
	if err != nil {
		return 0, fmt.Errorf("read the pid of the sway swaytest.Start ran: %w", err)
	}
	pid, err := strconv.Atoi(string(b))
	if err != nil {
		return 0, fmt.Errorf("parse sway's pid in %s: %w", name, err)
	}
	return pid, nil
}
