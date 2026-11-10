// Package acceptance runs barsdig against a real extracted RomFS that you
// supply. It is skipped unless BARSDIG_TEST_ROMFS points at one, and it only
// checks properties that must hold for any well-formed input (no expected
// counts or names). No game data is stored in this repository.
//
//	BARSDIG_TEST_ROMFS=/path/to/romfs go test ./internal/acceptance -v
//	BARSDIG_TEST_ALL=1 ...   also renders every cue (slow)
package acceptance

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"barsdig/internal/bars"
	"barsdig/internal/catalog"
	"barsdig/internal/render"
	"barsdig/internal/source"
)

func TestRealRomFS(t *testing.T) {
	root := os.Getenv("BARSDIG_TEST_ROMFS")
	if root == "" {
		t.Skip("BARSDIG_TEST_ROMFS not set")
	}
	src, err := source.NewDir(root)
	if err != nil {
		t.Fatal(err)
	}

	// Every bank parses and every cue name matches its stored CRC-32.
	var banks, entries, unverified int
	for _, dir := range catalog.DefaultRoots {
		err := src.Walk(dir, func(e source.FileEntry) error {
			if !strings.HasSuffix(e.Rel, ".bars") && !strings.HasSuffix(e.Rel, ".bars.zs") {
				return nil
			}
			banks++
			data, err := src.Read(e.Rel)
			if err != nil {
				t.Errorf("%s: %v", e.Rel, err)
				return nil
			}
			f, err := bars.Parse(data)
			if err != nil {
				t.Errorf("%s: %v", e.Rel, err)
				return nil
			}
			for _, en := range f.Entries {
				entries++
				if !en.NameVerified {
					unverified++
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%d banks, %d entries, %d unverified names", banks, entries, unverified)
	if banks == 0 {
		t.Fatal("no .bars files found: is this a RomFS with Sound/Resource or Voice/Resource?")
	}
	if unverified != 0 {
		t.Errorf("%d cue names do not match their stored CRC-32", unverified)
	}

	cat, err := catalog.Build(src, catalog.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d cues, %d problems", len(cat.Cues), len(cat.Problems))
	for i, p := range cat.Problems {
		if i < 10 {
			t.Logf("problem: %s (entry %d): %s", p.Path, p.Entry, p.Msg)
		}
		if strings.HasSuffix(p.Path, ".bars") || strings.HasSuffix(p.Path, ".bars.zs") {
			t.Errorf("bank problem: %+v", p)
		}
	}

	// Every prefetch stub is either replaced by its full stream or has no audio at all.
	var stubs, aliases, noAudio int
	for _, c := range cat.Cues {
		if c.NoAudio {
			noAudio++
		}
		if c.AliasOf != "" {
			aliases++
		}
		if c.PrefetchStub {
			stubs++
			if !c.UsesFullStream && !c.NoAudio {
				t.Errorf("%s: playable stub without a full stream", c.ID)
			}
		}
	}
	t.Logf("%d prefetch stubs, %d aliases, %d cues without audio", stubs, aliases, noAudio)

	// Sample cues of each codec render; BARSDIG_TEST_ALL renders everything.
	rd := render.New(src)
	byCodec := map[string][]*catalog.Cue{}
	for _, c := range cat.Cues {
		if !c.NoAudio {
			byCodec[c.Codec] = append(byCodec[c.Codec], c)
		}
	}
	magic := map[string]string{"dsp-adpcm": "RIFF", "pcm16": "RIFF", "opus": "OggS"}
	for codec, list := range byCodec {
		want, known := magic[codec]
		if !known {
			t.Logf("codec %q: %d cues not renderable (unsupported)", codec, len(list))
			continue
		}
		step := max(1, len(list)/100)
		done := 0
		for i := 0; i < len(list); i += step {
			a, err := rd.Cue(list[i])
			if err != nil {
				t.Errorf("%s: %v", list[i].ID, err)
				continue
			}
			if !bytes.HasPrefix(a.Data, []byte(want)) {
				t.Errorf("%s: output does not start with %s", list[i].ID, want)
			}
			done++
		}
		t.Logf("codec %s: rendered %d of %d cues", codec, done, len(list))
	}

	if os.Getenv("BARSDIG_TEST_ALL") != "" {
		failed := 0
		for codec, list := range byCodec {
			if _, ok := magic[codec]; !ok {
				continue
			}
			for _, c := range list {
				if _, err := rd.Cue(c); err != nil {
					failed++
					if failed <= 10 {
						t.Errorf("%s: %v", c.ID, err)
					}
				}
			}
		}
		t.Logf("rendered every renderable cue, %d failed", failed)
	}
}
