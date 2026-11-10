package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"barsdig/internal/source"
)

const cacheVersion = 1

type cacheFile struct {
	Version     int       `json:"version"`
	Fingerprint string    `json:"fingerprint"`
	Cues        []*Cue    `json:"cues"`
	Problems    []Problem `json:"problems"`
}

// fingerprint hashes the path, size and mtime of every file under the roots.
func fingerprint(src source.Source, roots []string) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "v%d\n", cacheVersion)
	for _, root := range roots {
		fmt.Fprintf(h, "root %s\n", root)
		err := src.Walk(root, func(e source.FileEntry) error {
			_, err := fmt.Fprintf(h, "%s %d %d\n", e.Rel, e.Size, e.ModTime)
			return err
		})
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Load returns the catalog, using the cache in cacheDir when its fingerprint
// matches the source and rebuilding (and rewriting it) otherwise. An empty
// cacheDir disables caching. Cache problems never fail the load.
func Load(src source.Source, opt Options, cacheDir string) (c *Catalog, fromCache bool, err error) {
	if cacheDir == "" {
		c, err = Build(src, opt)
		return c, false, err
	}
	roots := opt.Roots
	if len(roots) == 0 {
		roots = DefaultRoots
	}
	fp, err := fingerprint(src, roots)
	if err != nil {
		return nil, false, err
	}
	path := filepath.Join(cacheDir, "catalog-"+fp[:16]+".json")
	if b, err := os.ReadFile(path); err == nil {
		var cf cacheFile
		if json.Unmarshal(b, &cf) == nil && cf.Version == cacheVersion && cf.Fingerprint == fp && cf.Cues != nil {
			c = &Catalog{Cues: cf.Cues, Problems: cf.Problems}
			if c.Problems == nil {
				c.Problems = []Problem{}
			}
			c.index()
			return c, true, nil
		}
	}
	c, err = Build(src, opt)
	if err != nil {
		return nil, false, err
	}
	if b, err := json.Marshal(cacheFile{Version: cacheVersion, Fingerprint: fp, Cues: c.Cues, Problems: c.Problems}); err == nil {
		if os.MkdirAll(cacheDir, 0o755) == nil {
			if tmp, err := os.CreateTemp(cacheDir, "catalog-*.tmp"); err == nil {
				_, werr := tmp.Write(b)
				cerr := tmp.Close()
				if werr == nil && cerr == nil && os.Rename(tmp.Name(), path) == nil {
					return c, false, nil
				}
				os.Remove(tmp.Name())
			}
		}
	}
	return c, false, nil
}
