package source

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

type dirSource struct{ root string }

// NewDir returns a Source over a directory. Every path handed to it is
// validated: no absolute paths, no ".." elements, no backslashes, and no
// symlinks that lead outside the root.
func NewDir(root string) (Source, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("source: %s is not a directory", root)
	}
	return &dirSource{root: real}, nil
}

func validRel(rel string, allowEmpty bool) error {
	if rel == "" && allowEmpty {
		return nil
	}
	if rel == "" || strings.ContainsAny(rel, "\\\x00:") || strings.HasPrefix(rel, "/") {
		return fmt.Errorf("source: invalid path %q", rel)
	}
	for _, el := range strings.Split(rel, "/") {
		if el == ".." || el == "." || el == "" {
			return fmt.Errorf("source: invalid path %q", rel)
		}
	}
	return nil
}

// resolve maps rel to an absolute path and makes sure it stays inside root even through symlinks.
func (d *dirSource) resolve(rel string) (string, error) {
	if err := validRel(rel, false); err != nil {
		return "", err
	}
	full := filepath.Join(d.root, filepath.FromSlash(rel))
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	if real != d.root && !strings.HasPrefix(real, d.root+string(filepath.Separator)) {
		return "", fmt.Errorf("source: %q resolves outside the root", rel)
	}
	return real, nil
}

func (d *dirSource) Walk(root string, fn func(FileEntry) error) error {
	if err := validRel(root, true); err != nil {
		return err
	}
	start := filepath.Join(d.root, filepath.FromSlash(root))
	if fi, err := os.Stat(start); err != nil || !fi.IsDir() {
		return nil
	}
	return filepath.WalkDir(start, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.Type().IsRegular() { // directories are descended into; symlinks and specials are skipped
			return nil
		}
		fi, err := e.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(d.root, p)
		if err != nil {
			return err
		}
		return fn(FileEntry{Rel: path.Clean(filepath.ToSlash(rel)), Size: fi.Size(), ModTime: fi.ModTime().UnixNano()})
	})
}

func (d *dirSource) Stat(rel string) (FileEntry, error) {
	p, err := d.resolve(rel)
	if err != nil {
		return FileEntry{}, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return FileEntry{}, err
	}
	if !fi.Mode().IsRegular() {
		return FileEntry{}, fmt.Errorf("source: %q is not a regular file", rel)
	}
	return FileEntry{Rel: rel, Size: fi.Size(), ModTime: fi.ModTime().UnixNano()}, nil
}

func (d *dirSource) Read(rel string) ([]byte, error) {
	p, err := d.resolve(rel)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(rel, ".zs") {
		return raw, nil
	}
	var h zstd.Header
	if err := h.Decode(raw); err != nil {
		return nil, fmt.Errorf("source: %s: %w", rel, err)
	}
	if h.HasFCS && h.FrameContentSize > uint64(MaxDecompressed) {
		return nil, fmt.Errorf("source: %s decompresses to %d bytes, over the %d byte limit", rel, h.FrameContentSize, MaxDecompressed)
	}
	// A fresh decoder per call so the current limit is honoured (tests lower it).
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(uint64(MaxDecompressed)), zstd.WithDecoderMaxWindow(zstd.MaxWindowSize))
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	out, err := dec.DecodeAll(raw, nil)
	if err != nil {
		if errors.Is(err, zstd.ErrDecoderSizeExceeded) {
			return nil, fmt.Errorf("source: %s decompresses past the %d byte limit", rel, MaxDecompressed)
		}
		return nil, fmt.Errorf("source: %s: %w", rel, err)
	}
	return out, nil
}

func (d *dirSource) ReadHead(rel string, n int) ([]byte, error) {
	if n < 0 {
		return nil, errors.New("source: negative length")
	}
	if strings.HasSuffix(rel, ".zs") {
		b, err := d.Read(rel)
		if err != nil {
			return nil, err
		}
		if len(b) > n {
			b = b[:n]
		}
		return b, nil
	}
	p, err := d.resolve(rel)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	m, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return buf[:m], nil
}
