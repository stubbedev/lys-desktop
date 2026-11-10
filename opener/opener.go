// Package opener opens a file or URI with the user's default app, as
// xdg-open does without its desktop detection: the target's mimetype
// (x-scheme-handler/<scheme> for a URI, the shared-mime-info glob and
// magic answer for a file), the default app the mimeapps.list files name
// for it or its nearest parent type, launched through its desktop entry.
package opener

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/stubbedev/lys-desktop/desktopentry"
	"github.com/stubbedev/lys-desktop/fileuri"
	"github.com/stubbedev/lys-desktop/mime"
)

// Target is what Open opens: an absolute local path, or a URI with a
// scheme other than file.
type Target struct {
	path   string
	uri    string
	scheme string
}

// ParseTarget reads s as an absolute path, a file:// URI (taken as the
// path it names) or a URI with any other scheme.
func ParseTarget(s string) (Target, error) {
	if filepath.IsAbs(s) {
		return Target{path: s}, nil
	}
	name, ok := scheme(s)
	if !ok {
		return Target{}, fmt.Errorf("opener: %q is neither an absolute path nor a URI", s)
	}
	if name != "file" {
		return Target{uri: s, scheme: name}, nil
	}
	path, ok := fileuri.Path(s)
	if !ok {
		return Target{}, fmt.Errorf("opener: file URI %q names no path", s)
	}
	return Target{path: path}, nil
}

// String is the target as it was given: the path or the URI.
func (t Target) String() string {
	if t.path != "" {
		return t.path
	}
	return t.uri
}

func (t Target) launchURI() string {
	if t.path != "" {
		return fileuri.FromPath(t.path)
	}
	return t.uri
}

func scheme(s string) (string, bool) {
	name, _, ok := strings.Cut(s, ":")
	if !ok || name == "" || !isAlpha(name[0]) {
		return "", false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !isAlpha(c) && (c < '0' || c > '9') && c != '+' && c != '-' && c != '.' {
			return "", false
		}
	}
	return strings.ToLower(name), true
}

func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// NoHandlerError is a target whose mimetype, and every parent type of
// it, no installed app opens.
type NoHandlerError struct {
	Target   Target
	MIMEType string
}

func (e *NoHandlerError) Error() string {
	return fmt.Sprintf("opener: no application opens %s (%s)", e.Target, e.MIMEType)
}

// resolver picks the app a target opens with from one read of the
// mimetype database, the installed apps and the mimeapps.list files.
type resolver struct {
	mime  *mime.Database
	apps  []desktopentry.App
	lists desktopentry.MimeApps
}

func systemResolver() resolver {
	return resolver{
		mime:  mime.System(),
		apps:  desktopentry.AllApps(desktopentry.ApplicationDirs()),
		lists: desktopentry.ReadMimeApps(desktopentry.MimeAppsLists()),
	}
}

// mimeType is t's mimetype: x-scheme-handler/<scheme> for a URI, the
// file's type for a path.
func (r resolver) mimeType(t Target) (string, error) {
	if t.path == "" {
		return "x-scheme-handler/" + t.scheme, nil
	}
	return r.mime.TypeOfFile(t.path)
}

// handler is the app t opens with and t's mimetype: the default for the
// type, else for its nearest parent type; a *NoHandlerError when none
// has one.
func (r resolver) handler(t Target) (desktopentry.App, string, error) {
	mimeType, err := r.mimeType(t)
	if err != nil {
		return desktopentry.App{}, "", fmt.Errorf("opener: open %s: %w", t, err)
	}
	for _, candidate := range r.mime.Lineage(mimeType) {
		if app, ok := r.lists.Default(r.apps, candidate); ok {
			return app, mimeType, nil
		}
	}
	return desktopentry.App{}, mimeType, &NoHandlerError{Target: t, MIMEType: mimeType}
}

// open opens t with its handler in ctx.
func (r resolver) open(t Target, ctx desktopentry.LaunchContext) error {
	app, mimeType, err := r.handler(t)
	if err != nil {
		return err
	}
	if err := app.Open(t.launchURI(), ctx); err != nil {
		return fmt.Errorf("opener: open %s (%s) with %s: %w", t, mimeType, app.ID, err)
	}
	return nil
}

// Open parses target and opens it with the user's default app for it,
// from the apps and mimeapps.list files as they are now.
func Open(target string, ctx desktopentry.LaunchContext) error {
	t, err := ParseTarget(target)
	if err != nil {
		return err
	}
	return systemResolver().open(t, ctx)
}
