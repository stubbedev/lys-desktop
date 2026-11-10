package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/logind"
	"github.com/stubbedev/lys-lib/logind/logindtest"

	"github.com/stubbedev/lys-desktop/spawn"
)

const displaySession logind.SessionID = "2"

// defaultLockCommands are the power menu's and the dashboard's default
// Lock commands.
func defaultLockCommands() []struct{ name, command string } {
	return []struct{ name, command string }{
		{"power", DefaultsPower().LockCommand},
		{"dashboard", DefaultsDashboard().DropdownLockCommand},
	}
}

// The default Lock commands, run as the shell runs a button's command
// from the systemd user unit (no XDG_SESSION_ID, outside any session),
// lock the user's display session through the real loginctl.
func TestDefaultLockCommandsLockTheDisplaySession(t *testing.T) {
	for _, c := range defaultLockCommands() {
		command := c.command
		t.Run(c.name, func(t *testing.T) {
			fake := logindtest.StartUnit(t, logindtest.Config{Display: displaySession})
			out, err := spawn.Owned(t.Context(), command).CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v: %s", command, err, out)
			}
			want := logind.SessionPath(displaySession)
			if got := fake.CallsTo("Session.Lock"); len(got) != 1 || got[0].Path != want {
				t.Errorf("%s: Session.Lock calls %v, want one on %s", command, got, want)
			}
			if got := fake.Events.Load(); !slices.Equal(got, []logindtest.Event{logindtest.EventLock}) {
				t.Errorf("%s: logind events %v, want one lock", command, got)
			}
		})
	}
}

// With no display session for "auto" to resolve, the default Lock
// commands fail and lock nothing.
func TestDefaultLockCommandsWithoutASessionFail(t *testing.T) {
	for _, c := range defaultLockCommands() {
		command := c.command
		t.Run(c.name, func(t *testing.T) {
			fake := logindtest.StartUnit(t, logindtest.Config{})
			out, err := spawn.Owned(t.Context(), command).CombinedOutput()
			if err == nil || !strings.HasPrefix(string(out), "Failed to issue method call: ") {
				t.Errorf("%s: err %v output %q, want loginctl's failed Lock call", command, err, out)
			}
			if got := fake.Events.Load(); len(got) != 0 {
				t.Errorf("%s: logind events %v with no session", command, got)
			}
		})
	}
}
