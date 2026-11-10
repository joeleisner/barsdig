package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

const maxProblemsShown = 20

func cmdIndex(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("index", stderr)
	cacheDir := fs.String("cache-dir", defaultCacheDir(), "catalog cache directory (empty disables caching)")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return 2
	}
	romfs, ok := oneRomFS("index", pos, stderr)
	if !ok {
		return 2
	}
	_, cat, cached, err := openCatalog(romfs, *cacheDir)
	if err != nil {
		fmt.Fprintf(stderr, "barsdig: %v\n", err)
		return 1
	}

	banks := map[string]bool{}
	codecs := map[string]int{}
	var stubs, linked, noAudio, aliases int
	for _, c := range cat.Cues {
		banks[c.Bank] = true
		if c.Codec != "" {
			codecs[c.Codec]++
		}
		if c.PrefetchStub {
			stubs++
			if c.UsesFullStream {
				linked++
			}
		}
		if c.NoAudio {
			noAudio++
		}
		if c.AliasOf != "" {
			aliases++
		}
	}
	names := make([]string, 0, len(codecs))
	for k := range codecs {
		names = append(names, k)
	}
	sort.Strings(names)
	var cs []string
	for _, k := range names {
		cs = append(cs, fmt.Sprintf("%s %d", k, codecs[k]))
	}

	note := ""
	if cached {
		note = " (cached)"
	}
	fmt.Fprintf(stdout, "RomFS: %s%s\n", romfs, note)
	fmt.Fprintf(stdout, "Cues: %d\n", len(cat.Cues))
	fmt.Fprintf(stdout, "Banks: %d\n", len(banks))
	fmt.Fprintf(stdout, "Codecs: %s\n", strings.Join(cs, ", "))
	fmt.Fprintf(stdout, "Prefetch stubs: %d (full stream found for %d, short clip only for %d)\n", stubs, linked, stubs-linked)
	fmt.Fprintf(stdout, "Aliases: %d\n", aliases)
	fmt.Fprintf(stdout, "No audio: %d\n", noAudio)
	fmt.Fprintf(stdout, "Problems: %d\n", len(cat.Problems))
	for i, p := range cat.Problems {
		if i == maxProblemsShown {
			fmt.Fprintf(stdout, "  ... and %d more\n", len(cat.Problems)-maxProblemsShown)
			break
		}
		where := p.Path
		if p.Entry >= 0 {
			where = fmt.Sprintf("%s [entry %d]", p.Path, p.Entry)
		}
		fmt.Fprintf(stdout, "  %s: %s\n", where, p.Msg)
	}
	if len(cat.Problems) > 0 {
		return 1
	}
	return 0
}
