package ogg

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"barsdig/internal/bwav"
)

// TestRealOpusAcceptedByFFmpeg muxes real Opus BWAVs (mono, stereo, 6-channel)
// and checks ffprobe and an ffmpeg decode accept them. Optional: needs
// BARSDIG_TEST_ROMFS plus ffprobe and ffmpeg on PATH.
func TestRealOpusAcceptedByFFmpeg(t *testing.T) {
	root := os.Getenv("BARSDIG_TEST_ROMFS")
	if root == "" {
		t.Skip("BARSDIG_TEST_ROMFS not set")
	}
	ffprobe, e1 := exec.LookPath("ffprobe")
	ffmpeg, e2 := exec.LookPath("ffmpeg")
	if e1 != nil || e2 != nil {
		t.Skip("ffprobe/ffmpeg not on PATH")
	}
	want := map[int]string{}
	for _, dir := range []string{"Voice/Resource/Stream", "Sound/Resource/Stream"} {
		files, _ := filepath.Glob(filepath.Join(root, dir, "*.bwav"))
		for _, f := range files {
			h, err := os.ReadFile(f)
			if err != nil || len(h) < 0x12 || string(h[:4]) != "BWAV" || h[0x10] != 2 {
				continue
			}
			if ch := int(h[0x0E]); want[ch] == "" && len(h) < 2<<20 {
				want[ch] = f
			}
		}
	}
	for _, ch := range []int{1, 2, 6} {
		f := want[ch]
		if f == "" {
			t.Fatalf("no %d-channel Opus BWAV found", ch)
		}
		data, _ := os.ReadFile(f)
		st, err := bwav.OpusStreams(data)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var b bytes.Buffer
		if err := WriteOpus(&b, st); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "x.opus")
		os.WriteFile(out, b.Bytes(), 0o644)
		o, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "stream=codec_name,channels", "-of", "csv=p=0", out).CombinedOutput()
		if err != nil || strings.TrimSpace(string(o)) != "opus,"+string(rune('0'+ch)) {
			t.Errorf("%d ch: ffprobe = %q, %v", ch, o, err)
		}
		if o, err := exec.Command(ffmpeg, "-v", "error", "-i", out, "-f", "null", "-").CombinedOutput(); err != nil || len(o) > 0 {
			t.Errorf("%d ch: ffmpeg decode: %v %s", ch, err, o)
		}
	}
}
