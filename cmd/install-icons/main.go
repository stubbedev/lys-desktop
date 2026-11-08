// Command install-icons writes the bundled symbolic icons into a
// directory: the ones named, or all of them. Packaging runs it, so a
// program installs the icons it ships without carrying copies.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/stubbedev/lys-lib/atomicfile"

	"github.com/stubbedev/lys-desktop/resources"
)

func install(dir string, names []string) error {
	if len(names) == 0 {
		entries, err := fs.ReadDir(resources.Icons, resources.IconsDir)
		if err != nil {
			return fmt.Errorf("list the bundled icons: %w", err)
		}
		for _, e := range entries {
			names = append(names, strings.TrimSuffix(e.Name(), path.Ext(e.Name())))
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // an install tree, world-readable by design
		return fmt.Errorf("create %s: %w", dir, err)
	}
	for _, name := range names {
		file := name + ".svg"
		data, err := resources.Icons.ReadFile(path.Join(resources.IconsDir, file))
		if err != nil {
			return fmt.Errorf("icon %s: %w", name, err)
		}
		if err := atomicfile.WriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
			return fmt.Errorf("install icon %s: %w", name, err)
		}
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: install-icons <dir> [name...]")
		os.Exit(2)
	}
	if err := install(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "install-icons:", err)
		os.Exit(1)
	}
}
