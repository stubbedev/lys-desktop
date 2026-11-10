package mime

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"unicode/utf8"
)

// The types of the files that are not regular, and the fallback for
// contents nothing claims that read as text.
const (
	Directory   = "inode/directory"
	Fifo        = "inode/fifo"
	Socket      = "inode/socket"
	CharDevice  = "inode/chardevice"
	BlockDevice = "inode/blockdevice"
	PlainText   = "text/plain"
)

const textSniffLen = 4096

// TypeOfFile is the mimetype of the file at path, symlinks followed, in
// the shared-mime-info checking order: a file that is not regular is
// its inode type; a name exactly one type's best glob claims is that
// type; otherwise the contents decide: the highest-priority magic match,
// else PlainText for contents that read as text and Unknown for the
// rest, or the first of the name's glob types that is one of those.
func (db *Database) TypeOfFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("mime: type of %s: %w", path, err)
	}
	if t, ok := inodeType(info.Mode()); ok {
		return t, nil
	}
	types := db.globTypes(filepath.Base(path))
	if len(types) == 1 {
		return types[0], nil
	}
	magic := db.magicTable()
	if magic.err != nil {
		return "", fmt.Errorf("mime: type of %s: %w", path, magic.err)
	}
	data, err := readHead(path, max(magic.extent, textSniffLen))
	if err != nil {
		return "", fmt.Errorf("mime: type of %s: %w", path, err)
	}
	return db.typeOfContents(magic, types, data, int64(len(data)) < info.Size()), nil
}

func (db *Database) typeOfContents(magic magicTable, globbed []string, data []byte, truncated bool) string {
	found, ok := magic.sniff(data)
	if !ok {
		found = Unknown
		if looksLikeText(data, truncated) {
			found = PlainText
		}
	}
	for _, t := range globbed {
		if db.IsA(t, found) {
			return t
		}
	}
	return found
}

func inodeType(mode fs.FileMode) (string, bool) {
	switch {
	case mode.IsRegular():
		return "", false
	case mode.IsDir():
		return Directory, true
	case mode&fs.ModeNamedPipe != 0:
		return Fifo, true
	case mode&fs.ModeSocket != 0:
		return Socket, true
	case mode&fs.ModeCharDevice != 0:
		return CharDevice, true
	case mode&fs.ModeDevice != 0:
		return BlockDevice, true
	}
	return "", false
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // the file being typed
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, n)
	read, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:read], nil
}

func looksLikeText(data []byte, truncated bool) bool {
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size <= 1 {
			return truncated && !utf8.FullRune(data[i:])
		}
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' && r != '\f' && r != '\v' && r != '\b' && r != 0x1b {
			return false
		}
		i += size
	}
	return true
}
