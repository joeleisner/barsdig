package render

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"barsdig/internal/bwav"
	"barsdig/internal/catalog"
	"barsdig/internal/source"
	"barsdig/internal/synth"
)

func dsp(samples, full uint32, prefetch bool, frames int) []byte {
	return synth.BWAV(synth.BWAVSpec{Codec: 1, SampleRate: 48000, FullSamples: full, Samples: samples, Prefetch: prefetch,
		Channels: [][]byte{make([]byte, 8*frames)}})
}

func opus(packets int) []byte {
	var pk [][]byte
	for i := 0; i < packets; i++ {
		pk = append(pk, []byte{0xF8, 0xFF, 0xFE})
	}
	return synth.BWAV(synth.BWAVSpec{Codec: 2, SampleRate: 48000, FullSamples: uint32(960 * packets), Samples: uint32(960 * packets),
		Channels: [][]byte{synth.NXOpus(pk, 120)}})
}

// fixture writes files under a temp RomFS and returns a source, catalog and renderer.
func fixture(t *testing.T, files map[string][]byte) (source.Source, *catalog.Catalog, *Renderer) {
	t.Helper()
	root := t.TempDir()
	for rel, data := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src, err := source.NewDir(root)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Build(src, catalog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return src, cat, New(src)
}

func cue(t *testing.T, c *catalog.Catalog, id string) *catalog.Cue {
	t.Helper()
	x, ok := c.Get(id)
	if !ok {
		t.Fatalf("no cue %q", id)
	}
	return x
}

func dataLen(t *testing.T, wav []byte) int {
	t.Helper()
	for p := 12; p+8 <= len(wav); {
		sz := int(binary.LittleEndian.Uint32(wav[p+4:]))
		if string(wav[p:p+4]) == "data" {
			return sz
		}
		p += 8 + sz + sz%2
	}
	t.Fatal("no data chunk")
	return 0
}

func TestDSPCueRendersWAV(t *testing.T) {
	_, cat, r := fixture(t, map[string][]byte{
		"Sound/Resource/Obj_X.bars": synth.BARS([]synth.Entry{{Name: "SE_A", Channels: 1, BWAV: dsp(14, 14, false, 1)}}),
	})
	a, err := r.Cue(cue(t, cat, "Obj_X/SE_A"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Ext != "wav" || a.ContentType != "audio/wav" || a.Stub || !bytes.HasPrefix(a.Data, []byte("RIFF")) || dataLen(t, a.Data) != 28 {
		t.Fatalf("audio = %s %s stub=%v len=%d", a.Ext, a.ContentType, a.Stub, len(a.Data))
	}
}

func TestOpusCueRendersOgg(t *testing.T) {
	_, cat, r := fixture(t, map[string][]byte{
		"Sound/Resource/Obj_X.bars": synth.BARS([]synth.Entry{{Name: "Ambient_A", Channels: 1, BWAV: opus(3)}}),
	})
	a, err := r.Cue(cue(t, cat, "Obj_X/Ambient_A"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Ext != "opus" || a.ContentType != "audio/ogg" || !bytes.HasPrefix(a.Data, []byte("OggS")) {
		t.Fatalf("audio = %s %s % x", a.Ext, a.ContentType, a.Data[:4])
	}
}

func TestStandaloneCueRenders(t *testing.T) {
	_, cat, r := fixture(t, map[string][]byte{"Sound/Resource/Stream/S.bwav": dsp(28, 28, false, 2)})
	a, err := r.Cue(cue(t, cat, "Stream/S"))
	if err != nil || dataLen(t, a.Data) != 56 || a.Stub {
		t.Fatalf("err=%v stub=%v", err, a != nil && a.Stub)
	}
}

func TestStubUsesFullStream(t *testing.T) {
	_, cat, r := fixture(t, map[string][]byte{
		"Sound/Resource/B.bars":                synth.BARS([]synth.Entry{{Name: "Ambient_A", Channels: 1, BWAV: dsp(14, 28, true, 1)}}),
		"Sound/Resource/Stream/Ambient_A.bwav": dsp(28, 28, false, 2),
	})
	a, err := r.Cue(cue(t, cat, "B/Ambient_A"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Stub || dataLen(t, a.Data) != 56 {
		t.Fatalf("stub=%v data=%d (want full stream: 28 samples)", a.Stub, dataLen(t, a.Data))
	}
}

func TestStubWithoutStreamMarkedStub(t *testing.T) {
	_, cat, r := fixture(t, map[string][]byte{
		"Sound/Resource/B.bars": synth.BARS([]synth.Entry{{Name: "Ambient_A", Channels: 1, BWAV: dsp(14, 28, true, 1)}}),
	})
	a, err := r.Cue(cue(t, cat, "B/Ambient_A"))
	if err != nil || !a.Stub || dataLen(t, a.Data) != 28 {
		t.Fatalf("err=%v audio=%+v", err, a)
	}
}

func TestNoAudioErrors(t *testing.T) {
	_, cat, r := fixture(t, map[string][]byte{
		"Sound/Resource/B.bars": synth.BARS([]synth.Entry{{Name: "SE_Silent", Channels: 1}}),
	})
	if _, err := r.Cue(cue(t, cat, "B/SE_Silent")); !errors.Is(err, ErrNoAudio) {
		t.Fatalf("err = %v", err)
	}
}

func TestUnsupportedCodecErrors(t *testing.T) {
	bad := synth.BWAV(synth.BWAVSpec{Codec: 9, SampleRate: 48000, FullSamples: 14, Samples: 14, Channels: [][]byte{make([]byte, 8)}})
	_, cat, r := fixture(t, map[string][]byte{"Sound/Resource/Stream/X.bwav": bad})
	var ue *bwav.UnsupportedCodecError
	if _, err := r.Cue(cue(t, cat, "Stream/X")); !errors.As(err, &ue) || ue.Codec != 9 {
		t.Fatalf("err = %v", err)
	}
}

func TestAliasRendersSharedAudio(t *testing.T) {
	_, cat, r := fixture(t, map[string][]byte{
		"Sound/Resource/B.bars": synth.BARS([]synth.Entry{
			{Name: "SE_B", Channels: 1, BWAV: dsp(14, 14, false, 1)}, {Name: "SE_A", Channels: 1, AliasOf: 1}}),
	})
	a, err1 := r.Cue(cue(t, cat, "B/SE_A"))
	b, err2 := r.Cue(cue(t, cat, "B/SE_B"))
	if err1 != nil || err2 != nil || !bytes.Equal(a.Data, b.Data) {
		t.Fatalf("errs %v %v", err1, err2)
	}
}

func TestStaleCatalogDetected(t *testing.T) {
	_, cat, r := fixture(t, map[string][]byte{
		"Sound/Resource/B.bars": synth.BARS([]synth.Entry{{Name: "SE_A", Channels: 1, BWAV: dsp(14, 14, false, 1)}}),
	})
	c := *cue(t, cat, "B/SE_A")
	c.EntryIndex = 5
	if _, err := r.Cue(&c); err == nil {
		t.Fatal("out-of-range entry index accepted")
	}
	c.EntryIndex = 0
	c.Name = "SE_Other"
	if _, err := r.Cue(&c); err == nil {
		t.Fatal("entry name mismatch accepted")
	}
}
