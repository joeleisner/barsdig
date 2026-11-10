// Package bars parses the BARS 1.2 audio container and its AMTA v5 metadata
// blocks. See docs/format.md.
package bars

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"sort"
)

// NoOffset is stored in the BWAV offset slot of an entry that has no audio.
const NoOffset = 0xFFFFFFFF

const (
	versionBARS = 0x0102
	versionAMTA = 0x0500

	maxNameLen   = 4096     // longest cue or marker name accepted
	maxMetaBytes = 64 << 20 // decoded metadata (names, raw blobs, markers) allowed per file
)

var le = binary.LittleEndian

// Marker is one entry of an AMTA marker table.
type Marker struct {
	ID     uint32
	Name   string
	Start  uint32
	Length uint32
}

// AMTA is the decoded metadata block of one entry. Parts nobody has
// documented are kept as raw bytes so nothing is lost.
type AMTA struct {
	Version    uint16
	VolumeDB   float32
	Channels   uint8
	Markers    []Marker
	RawData    []byte // the DATA blob (block+0x24, length from its own size field)
	RawExtra   []byte // block bytes after the name's NUL, trailing zero padding trimmed
	MinfOffset uint32 // block-relative; 0 if absent
	TagOffset  uint32 // block-relative; 0 if absent
}

// Entry is one cue of a BARS file.
type Entry struct {
	Index        int // position in the file (hash order)
	Hash         uint32
	Name         string
	NameVerified bool   // CRC-32 of Name equals Hash
	BWAV         []byte // slice of the input; nil if the entry has no audio
	BWAVOffset   uint32 // raw offset slot value (NoOffset if none)
	AliasOf      int    // -1, or the lowest Index sharing this entry's BWAV when this is not that entry
	AMTA         AMTA
}

// File is a parsed BARS container.
type File struct {
	Version uint16
	Entries []Entry
}

// UnsupportedError reports a BARS or AMTA version this package does not read.
// AMTA is 0 when the BARS version was already unsupported.
type UnsupportedError struct{ BARS, AMTA uint16 }

func (e *UnsupportedError) Error() string {
	if e.AMTA == 0 {
		return fmt.Sprintf("unsupported BARS version %#04x (need %#04x)", e.BARS, versionBARS)
	}
	return fmt.Sprintf("unsupported AMTA version %#04x in BARS %#04x (need %#04x)", e.AMTA, e.BARS, versionAMTA)
}

func u32(d []byte, at int) (uint32, bool) {
	if at < 0 || at+4 > len(d) {
		return 0, false
	}
	return le.Uint32(d[at:]), true
}

func cstr(d []byte, at int) (string, int, bool) {
	if at < 0 || at >= len(d) {
		return "", 0, false
	}
	n := bytes.IndexByte(d[at:], 0)
	if n < 0 {
		return "", 0, false
	}
	return string(d[at : at+n]), at + n, true
}

// Parse reads a BARS 1.2 file with AMTA v5 metadata.
func Parse(data []byte) (*File, error) {
	if len(data) < 0x10 {
		return nil, fmt.Errorf("bars: file too short (%d bytes)", len(data))
	}
	if string(data[:4]) != "BARS" {
		return nil, fmt.Errorf("bars: bad magic %q", data[:4])
	}
	if data[8] != 0xFF || data[9] != 0xFE {
		return nil, fmt.Errorf("bars: unsupported byte-order mark % x (little-endian only)", data[8:10])
	}
	ver := le.Uint16(data[10:])
	if ver != versionBARS {
		return nil, &UnsupportedError{BARS: ver}
	}
	count := uint64(le.Uint32(data[12:]))
	if 0x10+12*count > uint64(len(data)) {
		return nil, fmt.Errorf("bars: entry count %d does not fit in %d bytes", count, len(data))
	}
	n := int(count)
	pairs := 0x10 + 4*n

	f := &File{Version: ver, Entries: make([]Entry, n)}
	budget := maxMetaBytes
	var offsets []int
	for i := range f.Entries {
		e := &f.Entries[i]
		e.Index = i
		e.AliasOf = -1
		e.Hash = le.Uint32(data[0x10+4*i:])
		amtaOff := int(le.Uint32(data[pairs+8*i:]))
		e.BWAVOffset = le.Uint32(data[pairs+8*i+4:])
		if err := parseAMTA(data, amtaOff, e, ver, &budget); err != nil {
			return nil, fmt.Errorf("bars: entry %d: %w", i, err)
		}
		if e.BWAVOffset != NoOffset && e.BWAVOffset != 0 {
			if int(e.BWAVOffset) >= len(data) {
				return nil, fmt.Errorf("bars: entry %d (%s): BWAV offset %#x past end of file", i, e.Name, e.BWAVOffset)
			}
			offsets = append(offsets, int(e.BWAVOffset))
		} else {
			e.BWAVOffset = NoOffset
		}
	}

	sort.Ints(offsets)
	uniq := offsets[:0]
	for _, o := range offsets {
		if len(uniq) == 0 || uniq[len(uniq)-1] != o {
			uniq = append(uniq, o)
		}
	}
	end := make(map[int]int, len(uniq)) // offset -> end of its block
	for i, o := range uniq {
		end[o] = len(data)
		if i+1 < len(uniq) {
			end[o] = uniq[i+1]
		}
	}
	first := map[uint32]int{}
	for i := range f.Entries {
		e := &f.Entries[i]
		if e.BWAVOffset == NoOffset {
			continue
		}
		e.BWAV = data[e.BWAVOffset:end[int(e.BWAVOffset)]]
		if j, ok := first[e.BWAVOffset]; ok {
			e.AliasOf = j
		} else {
			first[e.BWAVOffset] = i
		}
	}
	return f, nil
}

// parseAMTA decodes one block. budget is the per-file allowance for decoded metadata, decremented as it is used.
func parseAMTA(data []byte, off int, e *Entry, barsVer uint16, budget *int) error {
	if off < 0 || off+0x28 > len(data) {
		return fmt.Errorf("AMTA offset %#x out of range", off)
	}
	if string(data[off:off+4]) != "AMTA" {
		return fmt.Errorf("no AMTA magic at %#x", off)
	}
	if data[off+4] != 0xFF || data[off+5] != 0xFE {
		return fmt.Errorf("AMTA at %#x: bad byte-order mark", off)
	}
	av := le.Uint16(data[off+6:])
	if av != versionAMTA {
		return &UnsupportedError{BARS: barsVer, AMTA: av}
	}
	size := int(le.Uint32(data[off+8:]))
	if size < 0x28 || off+size > len(data) {
		return fmt.Errorf("AMTA at %#x: bad block size %#x", off, size)
	}
	b := data[off : off+size]
	e.AMTA.Version = av

	blob, _ := u32(b, 0x24)
	if blob < 4 || uint64(0x24)+uint64(blob) >= uint64(size) {
		return fmt.Errorf("AMTA at %#x: bad DATA blob size %#x", off, blob)
	}
	nameAt := 0x24 + int(blob)
	n := bytes.IndexByte(b[nameAt:], 0)
	if n < 0 {
		return fmt.Errorf("AMTA at %#x: unterminated name", off)
	}
	if n > maxNameLen {
		return fmt.Errorf("AMTA at %#x: cue name is %d bytes (limit %d)", off, n, maxNameLen)
	}
	nameBytes := b[nameAt : nameAt+n]
	nul := nameAt + n
	e.NameVerified = crc32.ChecksumIEEE(nameBytes) == e.Hash
	// Copy everything kept: sub-slices of b would keep the whole decompressed bank alive.
	e.Name = string(nameBytes)
	e.AMTA.RawData = bytes.Clone(b[0x24:nameAt])
	e.AMTA.RawExtra = bytes.Clone(bytes.TrimRight(b[nul+1:], "\x00"))
	if *budget -= len(e.Name) + len(e.AMTA.RawData) + len(e.AMTA.RawExtra); *budget < 0 {
		return fmt.Errorf("AMTA at %#x: metadata exceeds the %d byte per-file limit", off, maxMetaBytes)
	}
	if len(b) > 0x31 {
		e.AMTA.Channels = b[0x31]
	}
	dataRel, _ := u32(b, 0x10)
	if dataRel != 0 {
		bits, ok := u32(b, int(dataRel)+0xC)
		if !ok {
			return fmt.Errorf("AMTA at %#x: DATA offset %#x out of range", off, dataRel)
		}
		e.AMTA.VolumeDB = math.Float32frombits(bits)
	}
	e.AMTA.MinfOffset, _ = u32(b, 0x18)
	e.AMTA.TagOffset, _ = u32(b, 0x1C)

	if markRel, _ := u32(b, 0x14); markRel != 0 {
		cnt, ok := u32(b, int(markRel))
		if !ok || uint64(markRel)+4+16*uint64(cnt) > uint64(size) {
			return fmt.Errorf("AMTA at %#x: marker table out of range", off)
		}
		for i := 0; i < int(cnt); i++ {
			rec := int(markRel) + 4 + 16*i
			id, _ := u32(b, rec)
			no, _ := u32(b, rec+4)
			start, _ := u32(b, rec+8)
			length, _ := u32(b, rec+12)
			s, _, ok := cstr(b, rec+4+int(no))
			if !ok || len(s) > maxNameLen {
				return fmt.Errorf("AMTA at %#x: marker %d name out of range or over %d bytes", off, i, maxNameLen)
			}
			if *budget -= len(s) + 16; *budget < 0 {
				return fmt.Errorf("AMTA at %#x: metadata exceeds the %d byte per-file limit", off, maxMetaBytes)
			}
			e.AMTA.Markers = append(e.AMTA.Markers, Marker{ID: id, Name: s, Start: start, Length: length})
		}
	}
	return nil
}
