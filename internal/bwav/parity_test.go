package bwav

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestParityWithVgmstream compares our DSP-ADPCM/PCM16 decode with
// vgmstream-cli sample for sample. Optional: needs BARSDIG_TEST_ROMFS and
// vgmstream-cli on PATH, otherwise it is skipped.
func TestParityWithVgmstream(t *testing.T) {
	root := os.Getenv("BARSDIG_TEST_ROMFS")
	if root == "" {
		t.Skip("BARSDIG_TEST_ROMFS not set")
	}
	vgm, err := exec.LookPath("vgmstream-cli")
	if err != nil {
		t.Skip("vgmstream-cli not on PATH")
	}
	files, _ := filepath.Glob(filepath.Join(root, "Sound", "Resource", "Stream", "*.bwav"))
	checked := 0
	for _, f := range files {
		if checked >= 20 {
			break
		}
		data, err := os.ReadFile(f)
		if err != nil || len(data) > 4<<20 {
			continue
		}
		p, err := Decode(data)
		if err != nil {
			continue // Opus etc.
		}
		out := filepath.Join(t.TempDir(), "ref.wav")
		if b, err := exec.Command(vgm, "-i", "-o", out, f).CombinedOutput(); err != nil {
			t.Fatalf("%s: vgmstream-cli: %v\n%s", f, err, b)
		}
		ref, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		pcm := wavData(t, ref)
		if len(pcm) != 2*p.Channels*len(p.Samples[0]) {
			t.Errorf("%s: length %d bytes, ours %d", filepath.Base(f), len(pcm), 2*p.Channels*len(p.Samples[0]))
			continue
		}
		for i := range p.Samples[0] {
			for ch := 0; ch < p.Channels; ch++ {
				got := int16(binary.LittleEndian.Uint16(pcm[2*(i*p.Channels+ch):]))
				if got != p.Samples[ch][i] {
					t.Fatalf("%s: sample %d ch %d: vgmstream %d, ours %d", filepath.Base(f), i, ch, got, p.Samples[ch][i])
				}
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no files compared")
	}
	t.Logf("compared %d files", checked)
}

func wavData(t *testing.T, w []byte) []byte {
	t.Helper()
	for p := 12; p+8 <= len(w); {
		sz := int(binary.LittleEndian.Uint32(w[p+4:]))
		if string(w[p:p+4]) == "data" {
			return w[p+8 : p+8+sz]
		}
		p += 8 + sz + sz%2
	}
	t.Fatal("no data chunk")
	return nil
}
