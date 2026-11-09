// Package testhome runs a test binary against a scratch home, so no
// test (or a daemon it starts) reads or writes the real user's files:
// the config the shell scaffolds and loads, the notification history,
// the persisted state. Nor does it reach the real user's session: the
// compositor and display a test talks to are ones it started itself.
package testhome

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/wmipc"
)

const isolatedEnv = "LYS_TESTHOME"

var sessionEnv = [...]string{
	wmipc.SocketEnv,
	"WAYLAND_DISPLAY",
	"WAYLAND_SOCKET",
	"DISPLAY",
	"HYPRLAND_INSTANCE_SIGNATURE",
	"NIRI_SOCKET",
	"MANGO_INSTANCE_SIGNATURE",
	"SWAYSOCK",
	"I3SOCK",
	"XDG_CURRENT_DESKTOP",
}

// Run points HOME and every XDG per-user directory at a fresh temp
// directory, runs the tests, removes it, and exits with their code.
// Call it from TestMain.
func Run(m *testing.M) {
	home, err := os.MkdirTemp("", "lys-shell-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testhome:", err)
		os.Exit(1)
	}
	code := run(m, home)
	_ = os.RemoveAll(home)
	os.Exit(code)
}

func run(m *testing.M, home string) int {
	if err := Isolate(home); err != nil {
		fmt.Fprintln(os.Stderr, "testhome:", err)
		return 1
	}
	return m.Run()
}

// Isolate points HOME and the XDG per-user directories under home, pins
// the sound server to the scratch runtime directory's socket, which only
// a test's own fake serves, and creates the runtime directory, which
// must exist with mode 0700. It unsets every variable that advertises a
// running compositor, display or desktop, so only a session a helper
// such as swaytest.Start sets up is reachable, unless the process is a
// child an isolated test started: the session it inherits is then the
// test's own.
func Isolate(home string) error {
	if os.Getenv(isolatedEnv) == "" {
		for _, key := range sessionEnv {
			if err := os.Unsetenv(key); err != nil {
				return fmt.Errorf("unset %s: %w", key, err)
			}
		}
	}
	for key, value := range map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, "config"),
		"XDG_DATA_HOME":   filepath.Join(home, "data"),
		"XDG_STATE_HOME":  filepath.Join(home, "state"),
		"XDG_CACHE_HOME":  filepath.Join(home, "cache"),
		"XDG_RUNTIME_DIR": filepath.Join(home, "run"),
		"PULSE_SERVER":    "unix:" + filepath.Join(home, "run", "pulse", "native"),
		isolatedEnv:       home,
	} {
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, "run"), 0o700); err != nil {
		return fmt.Errorf("create the runtime dir: %w", err)
	}
	return nil
}

// WithoutSession returns environ, in os.Environ's KEY=value form, minus
// every variable Isolate unsets, for a child a test starts as a session
// of its own.
func WithoutSession(environ []string) []string {
	return slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		key, _, _ := strings.Cut(kv, "=")
		return slices.Contains(sessionEnv[:], key)
	})
}
