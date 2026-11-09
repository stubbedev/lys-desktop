package testhome

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/wmipc"

	"github.com/stubbedev/lys-desktop/service/lyswm"
)

func TestIsolatePointsEveryUserDirUnderHome(t *testing.T) {
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR", "PULSE_SERVER"} {
		t.Setenv(k, "/real/"+k)
	}
	t.Setenv(isolatedEnv, "")
	home := t.TempDir()
	if err := Isolate(home); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": home + "/config", "XDG_DATA_HOME": home + "/data",
		"XDG_STATE_HOME": home + "/state", "XDG_CACHE_HOME": home + "/cache", "XDG_RUNTIME_DIR": home + "/run",
		"PULSE_SERVER": "unix:" + home + "/run/pulse/native", isolatedEnv: home,
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if info, err := os.Stat(filepath.Join(home, "run")); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("runtime dir = %v, %v; want a 0700 directory", info, err)
	}
}

func TestIsolateFailsWhereTheRuntimeDirCannotBeMade(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", "")
	t.Setenv(isolatedEnv, "")
	if err := Isolate(file); err == nil {
		t.Error("a home that is a file isolated without error")
	}
}

func TestIsolateHidesTheHostsLysWM(t *testing.T) {
	t.Setenv(isolatedEnv, "")
	t.Setenv(wmipc.SocketEnv, "/run/user/1000/lys-wm/wayland-1.sock")
	if err := Isolate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if lyswm.IsRunning() {
		t.Errorf("lys-wm still reachable through %s=%q", wmipc.SocketEnv, lyswm.SocketPath())
	}
}

func TestIsolateUnsetsEverySessionVariable(t *testing.T) {
	t.Setenv(isolatedEnv, "")
	for _, k := range sessionEnv {
		t.Setenv(k, "host-"+k)
	}
	if err := Isolate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, k := range sessionEnv {
		if v, set := os.LookupEnv(k); set {
			t.Errorf("%s = %q survived Isolate", k, v)
		}
	}
}

func TestIsolateKeepsTheSessionAnIsolatedParentStarted(t *testing.T) {
	t.Setenv(isolatedEnv, t.TempDir())
	for _, k := range sessionEnv {
		t.Setenv(k, "test-"+k)
	}
	if err := Isolate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, k := range sessionEnv {
		if got := os.Getenv(k); got != "test-"+k {
			t.Errorf("%s = %q, want the parent test's %q", k, got, "test-"+k)
		}
	}
}

func TestWithoutSessionKeepsOnlyTheRest(t *testing.T) {
	environ := []string{"PATH=/bin", "WAYLAND_DISPLAY=wayland-1", wmipc.SocketEnv + "=/run/lys.sock", "WAYLAND_DISPLAYS=x", "SWAYSOCK", "HOME=/home/u"}
	got := WithoutSession(environ)
	if want := []string{"PATH=/bin", "WAYLAND_DISPLAYS=x", "HOME=/home/u"}; !slices.Equal(got, want) {
		t.Errorf("WithoutSession = %q, want %q", got, want)
	}
	if environ[1] != "WAYLAND_DISPLAY=wayland-1" {
		t.Errorf("WithoutSession changed its argument: %q", environ)
	}
}
