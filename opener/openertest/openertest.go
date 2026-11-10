// Package openertest gives a test a scratch desktop to open things on:
// the XDG directories under a temp root, a private session bus, and
// fake apps on PATH that record what they were launched with.
package openertest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
)

// Desktop is the scratch desktop: Root holds config/ (the user's
// mimeapps.list), data/applications and data/mime.
type Desktop struct {
	Root string
	bin  string
	out  string
}

// New points HOME, the XDG config and data directories, the current
// desktop ("") and PATH at a fresh scratch desktop for the test.
func New(t *testing.T) Desktop {
	t.Helper()
	dbustest.Session(t)
	root := t.TempDir()
	d := Desktop{Root: root, bin: filepath.Join(root, "bin"), out: filepath.Join(root, "out")}
	for _, dir := range []string{d.bin, d.out, d.Applications(), filepath.Join(root, "config")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_CONFIG_DIRS", filepath.Join(root, "sysconfig"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_DATA_DIRS", filepath.Join(root, "sysdata"))
	t.Setenv("XDG_CURRENT_DESKTOP", "")
	t.Setenv("PATH", d.bin+":/bin:/usr/bin:"+os.Getenv("PATH"))
	return d
}

// Applications is the user's applications directory.
func (d Desktop) Applications() string { return filepath.Join(d.Root, "data", "applications") }

// Write writes body to the path under Root, creating its directories.
func (d Desktop) Write(t *testing.T, rel, body string) string {
	t.Helper()
	path := filepath.Join(d.Root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Tool is a program on the scratch PATH that records its arguments and
// $XDG_ACTIVATION_TOKEN each time it runs.
type Tool struct {
	Name   string
	record string
}

// Tool installs the program name.
func (d Desktop) Tool(t *testing.T, name string) Tool {
	t.Helper()
	tool := Tool{Name: name, record: filepath.Join(d.out, name)}
	script := "#!/bin/sh\nprintf '%s|%s\\n' \"$*\" \"$XDG_ACTIVATION_TOKEN\" > \"" + tool.record + "\"\n"
	if err := os.WriteFile(filepath.Join(d.bin, name), []byte(script), 0o755); err != nil { //nolint:gosec // the fake app must be executable
		t.Fatal(err)
	}
	return tool
}

// Handler installs the program name and its desktop entry name.desktop
// declaring mimeType, run as `name %u`.
func (d Desktop) Handler(t *testing.T, name, mimeType string) Tool {
	t.Helper()
	tool := d.Tool(t, name)
	d.Write(t, filepath.Join("data", "applications", name+".desktop"),
		"[Desktop Entry]\nType=Application\nName="+name+"\nExec="+name+" %u\nMimeType="+mimeType+";\n")
	return tool
}

// Ran waits up to five seconds for the tool's run and returns
// "<arguments>|<activation token>".
func (tool Tool) Ran(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(tool.record); err == nil && strings.HasSuffix(string(data), "\n") {
			return strings.TrimSuffix(string(data), "\n")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never ran", tool.Name)
	return ""
}

// HasRun reports whether the tool has run yet.
func (tool Tool) HasRun() bool {
	_, err := os.Stat(tool.record)
	return err == nil
}
