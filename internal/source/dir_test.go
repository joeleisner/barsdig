package source

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func write(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func compress(t *testing.T, data []byte, streaming bool) []byte {
	t.Helper()
	var b bytes.Buffer
	w, err := zstd.NewWriter(&b)
	if err != nil {
		t.Fatal(err)
	}
	if !streaming {
		w.ResetContentSize(&b, int64(len(data)))
	}
	w.Write(data)
	w.Close()
	return b.Bytes()
}

func newSrc(t *testing.T) (Source, string) {
	t.Helper()
	root := t.TempDir()
	s, err := NewDir(root)
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}

func TestWalkLexicalAndRelative(t *testing.T) {
	s, root := newSrc(t)
	write(t, root, "Sound/Resource/b.bars", []byte("b"))
	write(t, root, "Sound/Resource/a.bars.zs", []byte("a"))
	write(t, root, "Sound/Resource/Stream/z.bwav", []byte("zz"))
	write(t, root, "Other/x", []byte("x"))
	var got []string
	var sizes []int64
	err := s.Walk("Sound/Resource", func(e FileEntry) error {
		got = append(got, e.Rel)
		sizes = append(sizes, e.Size)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Sound/Resource/Stream/z.bwav", "Sound/Resource/a.bars.zs", "Sound/Resource/b.bars"}
	// WalkDir order is lexical per directory by name: "Stream" < "a.bars.zs" < "b.bars" (uppercase sorts first)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("walk = %v, want %v", got, want)
	}
	if sizes[0] != 2 {
		t.Errorf("size = %d", sizes[0])
	}
}

func TestMissingRootEmptyWalk(t *testing.T) {
	s, _ := newSrc(t)
	called := false
	if err := s.Walk("Voice/Resource", func(FileEntry) error { called = true; return nil }); err != nil || called {
		t.Fatalf("err = %v, called = %v", err, called)
	}
}

func TestReadPlainStatReadHead(t *testing.T) {
	s, root := newSrc(t)
	write(t, root, "a/b.bin", []byte("0123456789"))
	if d, err := s.Read("a/b.bin"); err != nil || string(d) != "0123456789" {
		t.Fatalf("Read = %q, %v", d, err)
	}
	if d, err := s.ReadHead("a/b.bin", 4); err != nil || string(d) != "0123" {
		t.Fatalf("ReadHead = %q, %v", d, err)
	}
	if d, err := s.ReadHead("a/b.bin", 100); err != nil || string(d) != "0123456789" {
		t.Fatalf("ReadHead long = %q, %v", d, err)
	}
	e, err := s.Stat("a/b.bin")
	if err != nil || e.Rel != "a/b.bin" || e.Size != 10 || e.ModTime == 0 {
		t.Fatalf("Stat = %+v, %v", e, err)
	}
}

func TestReadZsDecompresses(t *testing.T) {
	s, root := newSrc(t)
	payload := bytes.Repeat([]byte("barsdig"), 1000)
	write(t, root, "x.bars.zs", compress(t, payload, false))
	if d, err := s.Read("x.bars.zs"); err != nil || !bytes.Equal(d, payload) {
		t.Fatalf("Read: %d bytes, %v", len(d), err)
	}
	if d, err := s.ReadHead("x.bars.zs", 7); err != nil || string(d) != "barsdig" {
		t.Fatalf("ReadHead = %q, %v", d, err)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	s, root := newSrc(t)
	write(t, root, "ok.bin", []byte("x"))
	for _, bad := range []string{"../x", "/etc/passwd", "a/../../x", "a/../ok.bin", `a\b`, "", ".", "a\x00b", "C:/x"} {
		if _, err := s.Read(bad); err == nil {
			t.Errorf("Read(%q) accepted", bad)
		}
		if _, err := s.Stat(bad); err == nil {
			t.Errorf("Stat(%q) accepted", bad)
		}
		if _, err := s.ReadHead(bad, 4); err == nil {
			t.Errorf("ReadHead(%q) accepted", bad)
		}
	}
	if err := s.Walk("../x", func(FileEntry) error { return nil }); err == nil {
		t.Error("Walk(../x) accepted")
	}
}

func TestSymlinkOutsideRootRejected(t *testing.T) {
	s, root := newSrc(t)
	outside := t.TempDir()
	write(t, outside, "secret", []byte("secret"))
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.Symlink(outside, filepath.Join(root, "dirlink")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := s.Read("link"); err == nil {
		t.Error("symlink to outside file readable")
	}
	if _, err := s.Read("dirlink/secret"); err == nil {
		t.Error("file under symlinked dir readable")
	}
	n := 0
	s.Walk("", func(FileEntry) error { n++; return nil })
	if n != 0 {
		t.Errorf("walk followed symlinks: %d files", n)
	}
}

func TestZsOverMaxRejected(t *testing.T) {
	old := MaxDecompressed
	MaxDecompressed = 4096
	defer func() { MaxDecompressed = old }()
	s, root := newSrc(t)
	big := bytes.Repeat([]byte{0}, 100_000)
	write(t, root, "fcs.zs", compress(t, big, false))   // frame header declares the size
	write(t, root, "stream.zs", compress(t, big, true)) // size unknown up front
	for _, f := range []string{"fcs.zs", "stream.zs"} {
		if _, err := s.Read(f); err == nil {
			t.Errorf("%s: oversized .zs accepted", f)
		}
	}
	small := bytes.Repeat([]byte{1}, 500)
	write(t, root, "small.zs", compress(t, small, false))
	if d, err := s.Read("small.zs"); err != nil || len(d) != 500 {
		t.Errorf("small .zs: %d bytes, %v", len(d), err)
	}
}

func TestZsHeaderClaimsHugeNoAllocation(t *testing.T) {
	s, root := newSrc(t)
	// magic, descriptor: single segment + 8-byte content size, size = 1<<40, then junk
	hdr := []byte{0x28, 0xB5, 0x2F, 0xFD, 0xE0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0x01, 0x00, 0x00}
	write(t, root, "bomb.zs", hdr)
	if _, err := s.Read("bomb.zs"); err == nil {
		t.Fatal("expected error")
	}
}
