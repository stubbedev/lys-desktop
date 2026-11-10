package opener

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/desktopentry"
	"github.com/stubbedev/lys-desktop/mime"
	"github.com/stubbedev/lys-desktop/opener/openertest"
)

type fixture struct {
	openertest.Desktop
	tools map[string]openertest.Tool
}

func (f fixture) app(t *testing.T, id, mimes, extra string) {
	t.Helper()
	f.Write(t, filepath.Join("data", "applications", id), "[Desktop Entry]\nType=Application\nName="+id+"\n"+extra+"MimeType="+mimes+"\n")
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	f := fixture{Desktop: openertest.New(t), tools: map[string]openertest.Tool{}}
	t.Setenv("XDG_CURRENT_DESKTOP", "lys-wm")
	f.Write(t, "data/mime/globs2", "50:text/x-python:*.py\n50:image/png:*.png\n")
	f.Write(t, "data/mime/subclasses", "text/x-python text/x-script\n")
	for _, name := range []string{"browser", "otherbrowser", "files", "editor", "viewer", "term"} {
		f.tools[name] = f.Tool(t, name)
	}
	f.app(t, "browser.desktop", "x-scheme-handler/https;", "Exec=browser %u\n")
	f.app(t, "otherbrowser.desktop", "x-scheme-handler/https;", "Exec=otherbrowser --new-tab=%u\n")
	f.app(t, "files.desktop", "inode/directory;", "Exec=files %F\n")
	f.app(t, "editor.desktop", "text/plain;", "Exec=editor\n")
	f.app(t, "viewer.desktop", "image/png;", "Exec=viewer %f\nTerminal=true\n")
	f.Write(t, "config/lys-wm-mimeapps.list", "[Default Applications]\nx-scheme-handler/https=otherbrowser.desktop;\n")
	f.Write(t, "config/mimeapps.list", "[Default Applications]\nx-scheme-handler/https=browser.desktop;\n")
	return f
}

func (f fixture) ran(t *testing.T, name string) string {
	t.Helper()
	return f.tools[name].Ran(t)
}

func testResolver() resolver {
	return resolver{
		mime:  mime.Load([]string{os.Getenv("XDG_DATA_HOME")}),
		apps:  desktopentry.AllApps(desktopentry.ApplicationDirs()),
		lists: desktopentry.ReadMimeApps(desktopentry.MimeAppsLists()),
	}
}

func TestParseTarget(t *testing.T) {
	for in, want := range map[string]Target{
		"/home/u/a b.txt":             {path: "/home/u/a b.txt"},
		"file:///home/u/a%20b.txt":    {path: "/home/u/a b.txt"},
		"https://example.com/?q=1":    {uri: "https://example.com/?q=1", scheme: "https"},
		"mailto:someone@example.com":  {uri: "mailto:someone@example.com", scheme: "mailto"},
		"x-scheme+1.a-b:rest":         {uri: "x-scheme+1.a-b:rest", scheme: "x-scheme+1.a-b"},
		"HTTPS://EXAMPLE.COM/Mixed":   {uri: "HTTPS://EXAMPLE.COM/Mixed", scheme: "https"},
		"file:///":                    {path: "/"},
		"/path/with:colon/in/it:here": {path: "/path/with:colon/in/it:here"},
	} {
		got, err := ParseTarget(in)
		if err != nil || got != want {
			t.Errorf("ParseTarget(%q) = %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "relative/path", "notes.txt", "1http://x", ":nothing", "has space:x", "file://host"} {
		if got, err := ParseTarget(in); err == nil {
			t.Errorf("ParseTarget(%q) = %+v, want an error", in, got)
		}
	}
}

func TestHandlerFollowsTheListsAndTheTypeLineage(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.Root, "some dir")
	script := filepath.Join(f.Root, "run.py")
	f.Write(t, "run.py", "print(1)\n")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := testResolver()
	for target, want := range map[string]struct{ app, mime string }{
		"https://example.com": {"otherbrowser.desktop", "x-scheme-handler/https"},
		dir:                   {"files.desktop", mime.Directory},
		script:                {"editor.desktop", "text/x-python"},
	} {
		tgt, err := ParseTarget(target)
		if err != nil {
			t.Fatal(err)
		}
		app, mimeType, err := r.handler(tgt)
		if err != nil || app.ID != want.app || mimeType != want.mime {
			t.Errorf("handler(%s) = %s %s %v, want %s %s", target, app.ID, mimeType, err, want.app, want.mime)
		}
	}
	t.Setenv("XDG_CURRENT_DESKTOP", "")
	if app, _, err := testResolver().handler(Target{uri: "https://example.com", scheme: "https"}); err != nil || app.ID != "browser.desktop" {
		t.Errorf("outside lys-wm https = %s %v, want the plain mimeapps.list default", app.ID, err)
	}
}

func TestNoHandlerNamesTheTargetAndItsType(t *testing.T) {
	newFixture(t)
	for target, mimeType := range map[string]string{
		"gopher://example.com": "x-scheme-handler/gopher",
		"/nonexistent-dir":     "",
	} {
		tgt, err := ParseTarget(target)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = testResolver().handler(tgt)
		var none *NoHandlerError
		if mimeType == "" {
			if err == nil || errors.As(err, &none) || !strings.Contains(err.Error(), target) {
				t.Errorf("a missing file = %v, want the stat error naming it", err)
			}
			continue
		}
		if !errors.As(err, &none) || none.MIMEType != mimeType || none.Target.String() != target {
			t.Fatalf("handler(%s) = %v, want a NoHandlerError for %s", target, err, mimeType)
		}
		if msg := err.Error(); !strings.Contains(msg, target) || !strings.Contains(msg, mimeType) {
			t.Errorf("message %q names neither the target nor the type", msg)
		}
	}
}

func TestOpenLaunchesTheHandlerWithTheTarget(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.Root, "a dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(f.Root, "run.py")
	f.Write(t, "run.py", "print(1)\n")

	if err := Open("https://example.com/?q=a b", desktopentry.LaunchContext{ActivationToken: "tok"}); err != nil {
		t.Fatal(err)
	}
	if got := f.ran(t, "otherbrowser"); got != "--new-tab=https://example.com/?q=a b|tok" {
		t.Errorf("browser ran with %q, want the URI inline and the token", got)
	}
	if err := Open("file://"+dir, desktopentry.LaunchContext{}); err != nil {
		t.Fatal(err)
	}
	if got := f.ran(t, "files"); got != dir+"|" {
		t.Errorf("file manager ran with %q, want the local path for %%F", got)
	}
	if err := Open(script, desktopentry.LaunchContext{}); err != nil {
		t.Fatal(err)
	}
	if got := f.ran(t, "editor"); got != script+"|" {
		t.Errorf("editor without a field code ran with %q, want the path appended", got)
	}
}

func TestOpenRunsTerminalHandlersInTheGivenTerminal(t *testing.T) {
	f := newFixture(t)
	img := filepath.Join(f.Root, "pic.png")
	f.Write(t, "pic.png", "\x89PNG")
	if err := Open(img, desktopentry.LaunchContext{}); err == nil || !strings.Contains(err.Error(), "terminal") || !strings.Contains(err.Error(), img) {
		t.Errorf("a terminal handler with no terminal = %v, want an error naming the terminal and the file", err)
	}
	if err := Open(img, desktopentry.LaunchContext{Terminal: "term"}); err != nil {
		t.Fatal(err)
	}
	if got := f.ran(t, "term"); got != "-e viewer "+img+"|" {
		t.Errorf("terminal ran with %q, want -e and the viewer's command", got)
	}
}

func TestOpenRefusesAURIForAnAppTakingOnlyFiles(t *testing.T) {
	f := newFixture(t)
	f.app(t, "local.desktop", "x-scheme-handler/sftp;", "Exec=viewer %f\n")
	err := Open("sftp://host/file", desktopentry.LaunchContext{})
	if err == nil || !strings.Contains(err.Error(), "local.desktop") || !strings.Contains(err.Error(), "sftp://host/file") {
		t.Errorf("a remote URI for a %%f app = %v, want an error naming the app and the URI", err)
	}
}
