package mime

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

const magicHeader = "MIME-Magic\x00\n"

type magicSection struct {
	priority int
	mime     string
	rules    []magicRule
}

type magicRule struct {
	indent   int
	offset   int
	rangeLen int
	value    []byte
	mask     []byte
}

type magicTable struct {
	sections []magicSection
	extent   int
	err      error
}

func (db *Database) magicTable() magicTable {
	db.magicOnce.Do(func() { db.magic = loadMagic(db.dirs) })
	return db.magic
}

func loadMagic(dirs []string) magicTable {
	var t magicTable
	for _, dir := range dirs {
		path := filepath.Join(dir, "mime", "magic")
		data, err := os.ReadFile(path) //nolint:gosec // a system data file
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err == nil {
			var sections []magicSection
			if sections, err = parseMagic(data); err == nil {
				t.sections = append(t.sections, sections...)
				continue
			}
		}
		return magicTable{err: fmt.Errorf("mime: read magic %s: %w", path, err)}
	}
	slices.SortStableFunc(t.sections, func(a, b magicSection) int { return b.priority - a.priority })
	for _, s := range t.sections {
		for _, r := range s.rules {
			t.extent = max(t.extent, r.extent())
		}
	}
	return t
}

type magicParser struct {
	data []byte
	pos  int
}

func parseMagic(data []byte) ([]magicSection, error) {
	if !bytes.HasPrefix(data, []byte(magicHeader)) {
		return nil, errors.New("no MIME-Magic header")
	}
	p := magicParser{data: data, pos: len(magicHeader)}
	var sections []magicSection
	for p.pos < len(p.data) {
		if p.data[p.pos] == '[' {
			s, err := p.section()
			if err != nil {
				return nil, err
			}
			sections = append(sections, s)
			continue
		}
		if len(sections) == 0 {
			return nil, fmt.Errorf("rule at byte %d before any section", p.pos)
		}
		r, err := p.rule()
		if err != nil {
			return nil, err
		}
		last := &sections[len(sections)-1]
		last.rules = append(last.rules, r)
	}
	return sections, nil
}

func (p *magicParser) section() (magicSection, error) {
	start := p.pos
	end := bytes.Index(p.data[p.pos:], []byte("]\n"))
	if end < 0 {
		return magicSection{}, fmt.Errorf("unterminated section header at byte %d", start)
	}
	header := p.data[p.pos+1 : p.pos+end]
	p.pos += end + 2
	prio, mime, ok := bytes.Cut(header, []byte(":"))
	if !ok || len(mime) == 0 {
		return magicSection{}, fmt.Errorf("malformed section header %q at byte %d", header, start)
	}
	priority, err := strconv.Atoi(string(prio))
	if err != nil {
		return magicSection{}, fmt.Errorf("section priority %q at byte %d: %w", prio, start, err)
	}
	return magicSection{priority: priority, mime: string(mime)}, nil
}

func (p *magicParser) number(what string) (int, error) {
	start := p.pos
	for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
		p.pos++
	}
	n, err := strconv.Atoi(string(p.data[start:p.pos]))
	if err != nil {
		return 0, fmt.Errorf("rule %s at byte %d: %w", what, start, err)
	}
	return n, nil
}

func (p *magicParser) expect(c byte) bool {
	if p.pos < len(p.data) && p.data[p.pos] == c {
		p.pos++
		return true
	}
	return false
}

func (p *magicParser) take(n int) ([]byte, error) {
	if n > len(p.data)-p.pos {
		return nil, fmt.Errorf("rule value of %d bytes at byte %d runs past the end", n, p.pos)
	}
	b := p.data[p.pos : p.pos+n]
	p.pos += n
	return b, nil
}

func (p *magicParser) rule() (magicRule, error) {
	start := p.pos
	r := magicRule{rangeLen: 1}
	if p.data[p.pos] != '>' {
		indent, err := p.number("indent")
		if err != nil {
			return r, err
		}
		r.indent = indent
	}
	if !p.expect('>') {
		return r, fmt.Errorf("rule at byte %d: no '>'", start)
	}
	offset, err := p.number("offset")
	if err != nil {
		return r, err
	}
	r.offset = offset
	if !p.expect('=') {
		return r, fmt.Errorf("rule at byte %d: no '='", start)
	}
	lenBytes, err := p.take(2)
	if err != nil {
		return r, err
	}
	n := int(binary.BigEndian.Uint16(lenBytes))
	if r.value, err = p.take(n); err != nil {
		return r, err
	}
	if p.expect('&') {
		if r.mask, err = p.take(n); err != nil {
			return r, err
		}
	}
	wordSize := 1
	if p.expect('~') {
		if wordSize, err = p.number("word size"); err != nil {
			return r, err
		}
	}
	if p.expect('+') {
		if r.rangeLen, err = p.number("range length"); err != nil {
			return r, err
		}
	}
	if !p.expect('\n') {
		return r, fmt.Errorf("rule at byte %d: trailing data before the newline", start)
	}
	if r.rangeLen < 1 {
		return r, fmt.Errorf("rule at byte %d: range length %d", start, r.rangeLen)
	}
	if (wordSize == 2 || wordSize == 4) && littleEndian && n%wordSize == 0 {
		r.value = swapWords(r.value, wordSize)
		if r.mask != nil {
			r.mask = swapWords(r.mask, wordSize)
		}
	}
	return r, nil
}

var littleEndian = binary.NativeEndian.Uint16([]byte{1, 0}) == 1

func swapWords(b []byte, size int) []byte {
	out := make([]byte, len(b))
	for i := 0; i < len(b); i += size {
		for j := range size {
			out[i+j] = b[i+size-1-j]
		}
	}
	return out
}

func (r magicRule) extent() int { return r.offset + r.rangeLen - 1 + len(r.value) }

func (r magicRule) matches(data []byte) bool {
	for at := r.offset; at < r.offset+r.rangeLen; at++ {
		if at+len(r.value) > len(data) {
			return false
		}
		if r.matchesAt(data[at : at+len(r.value)]) {
			return true
		}
	}
	return false
}

func (r magicRule) matchesAt(window []byte) bool {
	if r.mask == nil {
		return bytes.Equal(window, r.value)
	}
	for i, m := range r.mask {
		if window[i]&m != r.value[i]&m {
			return false
		}
	}
	return true
}

func (s magicSection) matches(data []byte) bool { return matchRules(s.rules, data) }

func matchRules(rules []magicRule, data []byte) bool {
	for i := 0; i < len(rules); {
		r := rules[i]
		end := i + 1
		for end < len(rules) && rules[end].indent > r.indent {
			end++
		}
		if r.matches(data) && (end == i+1 || matchRules(rules[i+1:end], data)) {
			return true
		}
		i = end
	}
	return false
}

func (t magicTable) sniff(data []byte) (string, bool) {
	for _, s := range t.sections {
		if s.matches(data) {
			return s.mime, true
		}
	}
	return "", false
}
