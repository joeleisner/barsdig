package bars

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRealBanks parses every decompressed .bars in $BARSDIG_TEST_BARS_DIR
// (optional; skipped when unset). No game data is stored in the repo.
func TestRealBanks(t *testing.T) {
	dir := os.Getenv("BARSDIG_TEST_BARS_DIR")
	if dir == "" {
		t.Skip("BARSDIG_TEST_BARS_DIR not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.bars"))
	if len(files) == 0 {
		t.Fatal("no .bars files found")
	}
	total, aliases := 0, 0
	for _, p := range files {
		d, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		f, err := Parse(d)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(p), err)
			continue
		}
		for _, e := range f.Entries {
			total++
			if !e.NameVerified {
				t.Errorf("%s[%d] %q: name does not match hash", filepath.Base(p), e.Index, e.Name)
			}
			if e.AliasOf >= 0 {
				aliases++
			}
			if e.BWAV != nil && string(e.BWAV[:4]) != "BWAV" {
				t.Errorf("%s[%d]: BWAV magic %q", filepath.Base(p), e.Index, e.BWAV[:4])
			}
		}
	}
	t.Logf("%d files, %d entries, %d aliases", len(files), total, aliases)
}
