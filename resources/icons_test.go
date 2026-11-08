package resources_test

import (
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/resources"
)

// brandIcons are the bundled logos that are no Lucide glyph: Simple
// Icons' brands, and the distro logos Simple Icons lacks.
var brandIcons = []string{"cm-cachyos-symbolic", "cm-nobara-symbolic"}

// The bundle is Lucide's UI glyphs and brand logos only: Lucide has no
// brands, so those are Simple Icons' and the distro logos it lacks.
func TestBundledIconsAreLucideOrBrands(t *testing.T) {
	entries, err := fs.ReadDir(resources.Icons, resources.IconsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), path.Ext(e.Name()))
		if strings.HasPrefix(name, "ld-") || strings.HasPrefix(name, "si-") || slices.Contains(brandIcons, name) {
			continue
		}
		t.Errorf("%s is neither Lucide nor a brand logo", name)
	}
}

// Each icon set the bundle draws from ships its licence beside it, and
// no licence outlives its set.
func TestBundledLicencesMatchTheSets(t *testing.T) {
	entries, err := os.ReadDir("icons")
	if err != nil {
		t.Fatal(err)
	}
	var licences []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-LICENSE") {
			licences = append(licences, e.Name())
		}
	}
	if want := []string{"LUCIDE-LICENSE", "SIMPLE-ICONS-LICENSE"}; !slices.Equal(licences, want) {
		t.Errorf("licences = %v, want %v", licences, want)
	}
}
