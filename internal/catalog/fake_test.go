package catalog

import (
	"errors"
	"sort"
	"strings"

	"barsdig/internal/source"
)

// memSource is an in-memory source.Source for tests.
type memSource struct {
	files map[string][]byte
	mtime map[string]int64
	reads int // Read and ReadHead calls
}

func newMem() *memSource { return &memSource{files: map[string][]byte{}, mtime: map[string]int64{}} }

func (m *memSource) put(rel string, data []byte) *memSource { m.files[rel] = data; return m }

func (m *memSource) Walk(root string, fn func(source.FileEntry) error) error {
	var names []string
	for k := range m.files {
		if root == "" || strings.HasPrefix(k, root+"/") {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	for _, k := range names {
		if err := fn(source.FileEntry{Rel: k, Size: int64(len(m.files[k])), ModTime: m.mod(k)}); err != nil {
			return err
		}
	}
	return nil
}

func (m *memSource) Stat(rel string) (source.FileEntry, error) {
	d, ok := m.files[rel]
	if !ok {
		return source.FileEntry{}, errors.New("not found")
	}
	return source.FileEntry{Rel: rel, Size: int64(len(d)), ModTime: m.mod(rel)}, nil
}

func (m *memSource) mod(rel string) int64 {
	if v, ok := m.mtime[rel]; ok {
		return v
	}
	return 1
}

func (m *memSource) Read(rel string) ([]byte, error) {
	m.reads++
	d, ok := m.files[rel]
	if !ok {
		return nil, errors.New("not found")
	}
	return d, nil
}

func (m *memSource) ReadHead(rel string, n int) ([]byte, error) {
	d, err := m.Read(rel)
	if err != nil {
		return nil, err
	}
	if len(d) > n {
		d = d[:n]
	}
	return d, nil
}
