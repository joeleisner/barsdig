package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"barsdig/internal/catalog"
	"barsdig/internal/export"
)

func readPicks(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var ids []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ids = append(ids, line)
	}
	return ids, sc.Err()
}

func cmdExport(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("export", stderr)
	cacheDir := fs.String("cache-dir", defaultCacheDir(), "catalog cache directory (empty disables caching)")
	out := fs.String("out", "", "output directory (required)")
	picks := fs.String("picks", "", "file with one cue ID per line (# comments allowed)")
	match := fs.String("match", "", "export cues whose name or bank contains all these words")
	bank := fs.String("bank", "", "export every cue of this bank (exact name)")
	force := fs.Bool("force", false, "overwrite existing files")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return 2
	}
	romfs, ok := oneRomFS("export", pos, stderr)
	if !ok {
		return 2
	}
	if *out == "" {
		fmt.Fprintln(stderr, "barsdig export: --out DIR is required")
		return 2
	}
	if *picks == "" && *match == "" && *bank == "" {
		fmt.Fprintln(stderr, "barsdig export: choose what to export with --picks FILE, --match TEXT or --bank NAME")
		return 2
	}
	outAbs, err := filepath.Abs(*out)
	if err != nil {
		fmt.Fprintf(stderr, "barsdig: %v\n", err)
		return 1
	}
	src, cat, _, err := openCatalog(romfs, *cacheDir)
	if err != nil {
		fmt.Fprintf(stderr, "barsdig: %v\n", err)
		return 1
	}

	failed, unknown := 0, 0
	seen := map[string]bool{}
	var cues []*catalog.Cue
	add := func(c *catalog.Cue) {
		if !seen[c.ID] {
			seen[c.ID] = true
			cues = append(cues, c)
		}
	}
	if *picks != "" {
		ids, err := readPicks(*picks)
		if err != nil {
			fmt.Fprintf(stderr, "barsdig: %v\n", err)
			return 1
		}
		for _, id := range ids {
			if c, ok := cat.Get(id); ok {
				add(c)
			} else {
				fmt.Fprintf(stdout, "FAILED %s: unknown cue\n", id)
				failed++
				unknown++
			}
		}
	}
	if *match != "" || *bank != "" {
		q := catalog.Query{Text: *match}
		if *bank != "" {
			q.Banks = []string{*bank}
		}
		for _, c := range cat.Search(q) {
			add(c)
		}
	}

	results := export.Run(src, cues, export.Options{OutDir: outAbs, Force: *force, ToolVersion: Version})
	done := 0
	for _, r := range results {
		if r.Err != nil {
			fmt.Fprintf(stdout, "FAILED %s: %v\n", r.CueID, r.Err)
			failed++
			continue
		}
		done++
		fmt.Fprintf(stdout, "exported %s\n", r.CueID)
	}
	fmt.Fprintf(stdout, "Exported %d of %d cues to %s\n", done, len(cues)+unknown, outAbs)
	if failed > 0 {
		return 1
	}
	return 0
}
