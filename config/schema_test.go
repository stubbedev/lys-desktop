package config

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/config/internal/schemadoc"
)

const testSchemaVersion = "0.0.0"

// TestSchemaDocsAreCurrent fails when schemadoc_gen.go is stale:
// run `go generate ./config`.
func TestSchemaDocsAreCurrent(t *testing.T) {
	want, err := schemadoc.Generate(".", "config")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(schemadoc.OutputName)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("schemadoc_gen.go is stale; run `go generate ./config`")
	}
}

// TestPropertyOrder pins the declaration order the docs read: kept for
// a struct's properties, absent from the JSON, and a map built without
// one falls back to its sorted keys without the hidden entry.
func TestPropertyOrder(t *testing.T) {
	props := withOrder(Schema{"b": Schema{}, "a": Schema{}}, "b", "a")
	if got := PropertyOrder(props); len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Errorf("order = %v", got)
	}
	if strings.Contains(marshalJSON(props), "order") {
		t.Error("the order reached the JSON")
	}
	plain := Schema{"z": 1, "y": 2, propsOrderKey: 3}
	if got := PropertyOrder(plain); len(got) != 2 || got[0] != "y" || got[1] != "z" {
		t.Errorf("fallback = %v", got)
	}
}

// TestGenerateSchemaIsIndented pins the form the committed schema file
// is written in without a formatter: two-space indentation, not the
// compact form the writer builds it from.
func TestGenerateSchemaIsIndented(t *testing.T) {
	got := GenerateSchema(testSchemaVersion)
	var compact, indented bytes.Buffer
	if err := json.Compact(&compact, []byte(got)); err != nil {
		t.Fatal(err)
	}
	if got == compact.String() {
		t.Fatal("the schema is compact")
	}
	if err := json.Indent(&indented, compact.Bytes(), "", "  "); err != nil {
		t.Fatal(err)
	}
	if got != indented.String() {
		t.Error("the schema is not indented by two spaces")
	}
	if !strings.HasPrefix(got, "{\n  \"$defs\": {\n    \"") {
		t.Errorf("the schema does not open with its sorted $defs: %q", got[:min(len(got), 40)])
	}
}
