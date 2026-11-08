package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-desktop/resources"
)

func TestInstallsTheNamedIconsOnly(t *testing.T) {
	dir := t.TempDir()
	if err := install(dir, []string{"ld-x-symbolic"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "ld-x-symbolic.svg" {
		t.Fatalf("installed %v, want ld-x-symbolic.svg alone", entries)
	}
}

func TestInstallsEveryIconWithoutNames(t *testing.T) {
	dir := t.TempDir()
	if err := install(dir, nil); err != nil {
		t.Fatal(err)
	}
	want, err := fs.ReadDir(resources.Icons, resources.IconsDir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("installed %d icons, the bundle has %d", len(got), len(want))
	}
}

func TestAnUnknownIconIsAnError(t *testing.T) {
	if err := install(filepath.Join(t.TempDir(), "d"), []string{"no-such-icon"}); err == nil {
		t.Fatal("installing an unknown icon succeeded")
	}
}
