package catalog

import (
	"os"
	"testing"
	"time"

	"barsdig/internal/source"
)

// TestRealRomFS builds a catalog from $BARSDIG_TEST_ROMFS (optional) and logs
// what it found. No game data is stored in the repo.
func TestRealRomFS(t *testing.T) {
	root := os.Getenv("BARSDIG_TEST_ROMFS")
	if root == "" {
		t.Skip("BARSDIG_TEST_ROMFS not set")
	}
	src, err := source.NewDir(root)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	c, err := Build(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var stubs, linked, noaudio, aliases, unverified int
	codecs := map[string]int{}
	for _, cue := range c.Cues {
		codecs[cue.Codec]++
		if cue.PrefetchStub {
			stubs++
		}
		if cue.UsesFullStream {
			linked++
		}
		if cue.NoAudio {
			noaudio++
		}
		if cue.AliasOf != "" {
			aliases++
		}
		if cue.BarsPath != "" && !cue.NameVerified {
			unverified++
		}
	}
	t.Logf("built in %v: %d cues, %d problems, stubs %d (linked %d), no-audio %d, alias %d, unverified names %d, codecs %v",
		time.Since(start), len(c.Cues), len(c.Problems), stubs, linked, noaudio, aliases, unverified, codecs)
	for i, p := range c.Problems {
		if i < 5 {
			t.Logf("problem: %+v", p)
		}
	}
	if unverified != 0 {
		t.Errorf("%d BARS cue names failed CRC verification", unverified)
	}
}
