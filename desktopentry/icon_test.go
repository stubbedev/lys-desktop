package desktopentry

import (
	"slices"
	"testing"
)

func TestIconLookup(t *testing.T) {
	user, system := t.TempDir(), t.TempDir()
	write(t, system, "vlc.desktop", "[Desktop Entry]\nType=Application\nName=VLC\nIcon=vlc\n")
	// The user dir shadows the system one.
	write(t, user, "mpv.desktop", "[Desktop Entry]\nType=Application\nIcon=mpv-user\n")
	write(t, system, "mpv.desktop", "[Desktop Entry]\nType=Application\nIcon=mpv\n")
	// Icon keys outside [Desktop Entry] do not count.
	write(t, system, "odd.desktop", "[Desktop Action new]\nIcon=wrong\n[Desktop Entry]\nType=Application\nName=Odd\n")
	dirs := []string{user, system}

	if icon, ok := Icon("vlc", dirs); !ok || icon != "vlc" {
		t.Errorf("vlc = %q, %v", icon, ok)
	}
	if icon, ok := Icon("vlc.desktop", dirs); !ok || icon != "vlc" {
		t.Errorf("suffixed id = %q, %v", icon, ok)
	}
	if icon, _ := Icon("mpv", dirs); icon != "mpv-user" {
		t.Errorf("mpv = %q, want the user entry", icon)
	}
	if _, ok := Icon("odd", dirs); ok {
		t.Error("an Icon outside [Desktop Entry] was read")
	}
	if _, ok := Icon("missing", dirs); ok {
		t.Error("a missing entry resolved")
	}
	if _, ok := Icon("", dirs); ok {
		t.Error("the empty id resolved")
	}
}

func TestDirsFollowXDG(t *testing.T) {
	for _, c := range []struct {
		home, dataHome, dataDirs string
		want                     []string
	}{
		{"/home/u", "/data/home", "/a:/b", []string{"/data/home/applications", "/a/applications", "/b/applications"}},
		{"/home/u", "", "", []string{"/home/u/.local/share/applications", "/usr/local/share/applications", "/usr/share/applications"}},
		{"/home/u", "relative", "/a:relative::/b", []string{"/home/u/.local/share/applications", "/a/applications", "/b/applications"}},
		{"", "", "relative", []string{"/usr/local/share/applications", "/usr/share/applications"}},
	} {
		t.Setenv("HOME", c.home)
		t.Setenv("XDG_DATA_HOME", c.dataHome)
		t.Setenv("XDG_DATA_DIRS", c.dataDirs)
		if got := ApplicationDirs(); !slices.Equal(got, c.want) {
			t.Errorf("HOME=%q XDG_DATA_HOME=%q XDG_DATA_DIRS=%q: ApplicationDirs = %q, want %q", c.home, c.dataHome, c.dataDirs, got, c.want)
		}
	}
}
