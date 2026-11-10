// Package source reads files from an extracted RomFS. The Source interface is
// the seam that would let a .nup-backed source be added later.
package source

// FileEntry describes one file.
type FileEntry struct {
	Rel     string // slash-separated path relative to the source root
	Size    int64
	ModTime int64 // unix nanoseconds
}

// Source gives read access to a tree of files by relative path.
type Source interface {
	// Walk calls fn for every regular file under the relative directory root,
	// in lexical order. A missing root is an empty walk, not an error.
	Walk(root string, fn func(FileEntry) error) error
	Stat(rel string) (FileEntry, error)
	// Read returns the file's bytes; paths ending ".zs" are zstd-decompressed.
	Read(rel string) ([]byte, error)
	// ReadHead returns the first n bytes of the (decompressed) file, or fewer if it is shorter.
	ReadHead(rel string, n int) ([]byte, error)
}

// MaxDecompressed bounds the size of a decompressed ".zs" file.
var MaxDecompressed int64 = 512 << 20
