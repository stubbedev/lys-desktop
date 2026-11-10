package mime

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

type testRule struct {
	indent, offset, wordSize, rangeLen int
	value, mask                        string
}

func (r testRule) encode() string {
	var b strings.Builder
	if r.indent > 0 {
		b.WriteString(strconv.Itoa(r.indent))
	}
	b.WriteString(">" + strconv.Itoa(r.offset) + "=")
	b.Write(binary.BigEndian.AppendUint16(nil, uint16(len(r.value))))
	b.WriteString(r.value)
	if r.mask != "" {
		b.WriteString("&" + r.mask)
	}
	if r.wordSize > 0 {
		b.WriteString("~" + strconv.Itoa(r.wordSize))
	}
	if r.rangeLen > 0 {
		b.WriteString("+" + strconv.Itoa(r.rangeLen))
	}
	b.WriteString("\n")
	return b.String()
}

func magicFile(sections map[string][]testRule, order ...string) string {
	var b strings.Builder
	b.WriteString(magicHeader)
	for _, header := range order {
		b.WriteString("[" + header + "]\n")
		for _, r := range sections[header] {
			b.WriteString(r.encode())
		}
	}
	return b.String()
}

func writeDB(t *testing.T, files map[string]string) *Database {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "mime"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, "mime", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Load([]string{dir})
}

func sniffFixture(t *testing.T) *Database {
	t.Helper()
	sections := map[string][]testRule{
		"50:image/png": {{value: "\x89PNG"}},
		"60:application/x-nested": {
			{value: "NEST"},
			{indent: 1, offset: 8, value: "kid"},
		},
		"40:application/x-masked": {{value: "\xF0", mask: "\xF0"}},
		"45:application/x-ranged": {{offset: 4, rangeLen: 8, value: "FAR"}},
		"55:application/x-word":   {{value: "\x01\x02\x03\x04", wordSize: 4}},
		"80:application/msword":   {{value: "\xD0\xCF\x11\xE0"}},
		"30:text/x-lowprio":       {{value: "\x89PN"}},
	}
	magic := magicFile(sections, "80:application/msword", "60:application/x-nested", "55:application/x-word", "50:image/png", "45:application/x-ranged", "40:application/x-masked", "30:text/x-lowprio")
	return writeDB(t, map[string]string{
		"globs2":     "50:image/png:*.png\n50:application/msword:*.doc\n50:text/x-doc:*.doc\n50:text/plain:*.txt\n",
		"subclasses": "application/x-word-subtype application/msword\n",
		"magic":      magic,
	})
}

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTypeOfFile(t *testing.T) {
	db := sniffFixture(t)
	dir := t.TempDir()
	cases := map[string]struct{ name, body string }{
		"image/png":            {"fake.png", "plain text, but one glob claims the name"},
		"text/plain":           {"notes", "hello\nworld\t\x1b[0m\n"},
		Unknown:                {"blob", "\x00\x01\x02binary"},
		"application/x-nested": {"nested", "NEST....kid"},
		"application/x-masked": {"masked", "\xF7rest"},
		"application/x-ranged": {"ranged", "........FAR"},
		"application/msword":   {"word.doc", "\xD0\xCF\x11\xE0 ole"},
		"text/x-doc":           {"text.doc", "a doc that is text"},
	}
	for want, c := range cases {
		path := writeFile(t, dir, c.name, c.body)
		got, err := db.TypeOfFile(path)
		if err != nil || got != want {
			t.Errorf("TypeOfFile(%s) = %q %v, want %q", c.name, got, err, want)
		}
	}
	if got, err := db.TypeOfFile(writeFile(t, dir, "empty", "")); err != nil || got != PlainText {
		t.Errorf("an empty file = %q %v, want %q", got, err, PlainText)
	}
	if got, err := db.TypeOfFile(writeFile(t, dir, "png-magic", "\x89PNG\r\n")); err != nil || got != "image/png" {
		t.Errorf("a nameless PNG = %q %v, want image/png: the higher-priority section wins", got, err)
	}
}

func TestTypeOfFileRejectsWhatMagicDoesNotMatch(t *testing.T) {
	db := sniffFixture(t)
	dir := t.TempDir()
	for name, body := range map[string]string{
		"nested-without-child": "NEST....xyz",
		"masked-low-nibble":    "\x0Frest",
		"ranged-too-far":       "............FAR",
		"short":                "NE",
	} {
		got, err := db.TypeOfFile(writeFile(t, dir, name, body))
		if err != nil {
			t.Fatal(err)
		}
		if got != PlainText && got != Unknown {
			t.Errorf("%s = %q, want no magic match", name, got)
		}
	}
}

func TestTypeOfFileSwapsWordsOnLittleEndianHosts(t *testing.T) {
	db := sniffFixture(t)
	body := "\x01\x02\x03\x04"
	if littleEndian {
		body = "\x04\x03\x02\x01"
	}
	if got, err := db.TypeOfFile(writeFile(t, t.TempDir(), "word", body)); err != nil || got != "application/x-word" {
		t.Errorf("a host-order word = %q %v, want application/x-word", got, err)
	}
}

func TestTypeOfFileOfInodes(t *testing.T) {
	db := sniffFixture(t)
	dir := t.TempDir()
	if got, err := db.TypeOfFile(dir); err != nil || got != Directory {
		t.Errorf("a directory = %q %v", got, err)
	}
	fifo := filepath.Join(dir, "pipe.png")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := db.TypeOfFile(fifo); err != nil || got != Fifo {
		t.Errorf("a fifo named like a PNG = %q %v, want %q, never opened", got, err, Fifo)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if got, err := db.TypeOfFile(link); err != nil || got != Directory {
		t.Errorf("a symlink to a directory = %q %v", got, err)
	}
	missing := filepath.Join(dir, "missing")
	if _, err := db.TypeOfFile(missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("a missing file = %v, want an error naming it", err)
	}
}

func TestMalformedMagicFailsOnlyTheSniff(t *testing.T) {
	for name, magic := range map[string]string{
		"no header":      "[50:image/png]\n>0=\x00\x01x\n",
		"truncated":      magicHeader + "[50:image/png]\n>0=\x00\x09x\n",
		"rule first":     magicHeader + ">0=\x00\x01x\n",
		"no newline":     magicHeader + "[50:image/png]\n>0=\x00\x01xjunk\n",
		"bad priority":   magicHeader + "[high:image/png]\n>0=\x00\x01x\n",
		"zero range len": magicHeader + "[50:image/png]\n>0=\x00\x01x+0\n",
	} {
		db := writeDB(t, map[string]string{"globs2": "50:image/png:*.png\n", "magic": magic})
		dir := t.TempDir()
		if got, err := db.TypeOfFile(writeFile(t, dir, "a.png", "x")); err != nil || got != "image/png" {
			t.Errorf("%s: a single glob = %q %v, needs no magic", name, got, err)
		}
		if _, err := db.TypeOfFile(writeFile(t, dir, "nameless", "x")); err == nil || !strings.Contains(err.Error(), "magic") {
			t.Errorf("%s: a sniff = %v, want the magic file's error", name, err)
		}
	}
}

func TestLineage(t *testing.T) {
	db := writeDB(t, map[string]string{
		"aliases":    "application/x-alias application/x-real\n",
		"subclasses": "application/x-real application/xml\napplication/xml text/plain\ntext/x-python text/x-script\ntext/x-script application/x-executable\n",
	})
	for mimeType, want := range map[string][]string{
		"application/x-alias": {"application/x-real", "application/xml", "text/plain"},
		"text/x-python":       {"text/x-python", "text/x-script", "application/x-executable", "text/plain"},
		"text/x-orphan":       {"text/x-orphan", "text/plain"},
		"image/png":           {"image/png"},
		"text/plain":          {"text/plain"},
	} {
		if got := db.Lineage(mimeType); !slices.Equal(got, want) {
			t.Errorf("Lineage(%s) = %q, want %q", mimeType, got, want)
		}
	}
}
