package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"barsdig/internal/synth"
)

func cacheFixture() *memSource {
	return newMem().
		put("Sound/Resource/B.bars", synth.BARS([]synth.Entry{{Name: "SE_A", Channels: 1, BWAV: dspBWAV(14, 14, false, nil)}})).
		put("Sound/Resource/Stream/S.bwav", dspBWAV(96000, 96000, false, nil))
}

func TestLoadBuildsThenCaches(t *testing.T) {
	m, dir := cacheFixture(), t.TempDir()
	c1, from, err := Load(m, Options{}, dir)
	if err != nil || from {
		t.Fatalf("first load: from=%v err=%v", from, err)
	}
	m.reads = 0
	c2, from, err := Load(m, Options{}, dir)
	if err != nil || !from {
		t.Fatalf("second load: from=%v err=%v", from, err)
	}
	if m.reads != 0 {
		t.Errorf("cached load read %d files", m.reads)
	}
	if !reflect.DeepEqual(c1.Cues, c2.Cues) || len(c2.Cues) != 2 {
		t.Fatal("cached catalog differs")
	}
	if _, ok := c2.Get("B/SE_A"); !ok {
		t.Fatal("Get after cache load failed")
	}
}

func TestCacheInvalidatedWhenFileChanges(t *testing.T) {
	m, dir := cacheFixture(), t.TempDir()
	Load(m, Options{}, dir)
	m.mtime["Sound/Resource/Stream/S.bwav"] = 99 // mtime change
	if _, from, _ := Load(m, Options{}, dir); from {
		t.Error("mtime change did not invalidate the cache")
	}
	m.put("Sound/Resource/Stream/T.bwav", dspBWAV(14, 14, false, nil)) // new file
	if c, from, _ := Load(m, Options{}, dir); from || len(c.Cues) != 3 {
		t.Errorf("new file: from=%v cues=%d", from, len(c.Cues))
	}
	m.put("Sound/Resource/Stream/S.bwav", append(dspBWAV(14, 14, false, nil), make([]byte, 64)...)) // size change
	c, from, _ := Load(m, Options{}, dir)
	if s, ok := c.Get("Stream/S"); from || !ok || s.Samples != 14 {
		t.Errorf("size change not picked up (from=%v)", from)
	}
}

func TestCorruptCacheRebuilds(t *testing.T) {
	m, dir := cacheFixture(), t.TempDir()
	Load(m, Options{}, dir)
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(files) != 1 {
		t.Fatalf("cache files: %v", files)
	}
	os.WriteFile(files[0], []byte("{not json"), 0o644)
	c, from, err := Load(m, Options{}, dir)
	if err != nil || from || len(c.Cues) != 2 {
		t.Fatalf("from=%v err=%v cues=%d", from, err, len(c.Cues))
	}
	if _, from, _ := Load(m, Options{}, dir); !from {
		t.Error("cache not rewritten after corruption")
	}
}

func TestLoadWithoutCacheDir(t *testing.T) {
	c, from, err := Load(cacheFixture(), Options{}, "")
	if err != nil || from || len(c.Cues) != 2 {
		t.Fatalf("from=%v err=%v", from, err)
	}
}
