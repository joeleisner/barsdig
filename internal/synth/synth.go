// Package synth builds synthetic BARS and BWAV files for tests. The bytes it
// produces follow the layouts in docs/format.md; none of it is game data.
package synth

import (
	"encoding/binary"
	"hash/crc32"
	"math"
	"sort"
)

var le = binary.LittleEndian

// CRC returns the CRC-32/IEEE of a cue name, as stored in a BARS hash table.
func CRC(name string) uint32 { return crc32.ChecksumIEEE([]byte(name)) }

func align(n, a int) int { return (n + a - 1) / a * a }

// BWAVSpec describes a synthetic BWAV file.
type BWAVSpec struct {
	Codec       uint16
	SampleRate  uint32
	FullSamples uint32
	Samples     uint32
	Prefetch    bool
	Loop        *[2]int32 // start, end; nil stores loop end -1
	Coefs       [][16]int16
	Hist        [][3]int16 // predictor, hist1, hist2 per channel
	Channels    [][]byte   // raw per-channel data
}

// BWAV builds a BWAV file: a 0x10 byte header, 0x4C bytes per channel, then
// each channel's data aligned to 0x40.
func BWAV(s BWAVSpec) []byte {
	n := len(s.Channels)
	hdr := 0x10 + 0x4C*n
	offs := make([]int, n)
	pos := align(hdr, 0x40)
	for i, c := range s.Channels {
		offs[i] = pos
		pos = align(pos+len(c), 0x40)
	}
	d := make([]byte, pos)
	copy(d, "BWAV")
	d[4], d[5] = 0xFF, 0xFE
	le.PutUint16(d[6:], 1)
	if s.Prefetch {
		le.PutUint16(d[0x0C:], 1)
	}
	le.PutUint16(d[0x0E:], uint16(n))
	for i := 0; i < n; i++ {
		ch := d[0x10+0x4C*i:]
		le.PutUint16(ch[0:], s.Codec)
		le.PutUint16(ch[2:], uint16(i))
		le.PutUint32(ch[4:], s.SampleRate)
		le.PutUint32(ch[8:], s.FullSamples)
		le.PutUint32(ch[0xC:], s.Samples)
		if i < len(s.Coefs) {
			for k, v := range s.Coefs[i] {
				le.PutUint16(ch[0x10+2*k:], uint16(v))
			}
		}
		le.PutUint32(ch[0x34:], uint32(offs[i]))
		le.PutUint32(ch[0x38:], 1)
		loopEnd, loopStart := int32(-1), int32(-1)
		if s.Loop != nil {
			loopStart, loopEnd = s.Loop[0], s.Loop[1]
		}
		le.PutUint32(ch[0x3C:], uint32(loopEnd))
		le.PutUint32(ch[0x40:], uint32(loopStart))
		if i < len(s.Hist) {
			for k, v := range s.Hist[i] {
				le.PutUint16(ch[0x44+2*k:], uint16(v))
			}
		}
		copy(d[offs[i]:], s.Channels[i])
	}
	return d
}

// Marker is an AMTA marker record.
type Marker struct {
	ID, Start, Length uint32
	Name              string
}

// Entry describes one BARS entry.
type Entry struct {
	Name     string
	VolumeDB float32
	Channels byte
	Markers  []Marker
	BWAV     []byte // nil = no audio (offset 0xFFFFFFFF)
	// AliasOf is the 1-based index (into the slice passed to BARS) of the entry
	// whose BWAV this entry shares; 0 means none. BWAV is ignored when set.
	AliasOf int
}

func amtaBlock(e Entry) []byte {
	name := []byte(e.Name)
	b := make([]byte, 0x4C, 0x4C+len(name)+1+16*len(e.Markers)+64)
	copy(b, "AMTA")
	b[4], b[5] = 0xFF, 0xFE
	le.PutUint16(b[6:], 0x0500)
	le.PutUint32(b[0x10:], 0x34)
	le.PutUint32(b[0x24:], 0x28)
	le.PutUint32(b[0x28:], CRC(e.Name))
	le.PutUint32(b[0x2C:], 3)
	b[0x30], b[0x31], b[0x33] = 1, e.Channels, 4
	le.PutUint32(b[0x34:], 0x4F)
	le.PutUint32(b[0x40:], math.Float32bits(e.VolumeDB))
	b = append(b, name...)
	b = append(b, 0)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	if len(e.Markers) > 0 {
		markOff := len(b)
		le.PutUint32(b[0x14:], uint32(markOff))
		tbl := make([]byte, 4+16*len(e.Markers))
		le.PutUint32(tbl, uint32(len(e.Markers)))
		var strs []byte
		strBase := len(tbl)
		for i, m := range e.Markers {
			rec := tbl[4+16*i:]
			le.PutUint32(rec[0:], m.ID)
			// name offset is relative to the position of this field (rec+4)
			le.PutUint32(rec[4:], uint32(strBase+len(strs)-(4+16*i+4)))
			le.PutUint32(rec[8:], m.Start)
			le.PutUint32(rec[12:], m.Length)
			strs = append(strs, m.Name...)
			strs = append(strs, 0)
		}
		b = append(b, tbl...)
		b = append(b, strs...)
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
	}
	le.PutUint32(b[8:], uint32(len(b)))
	return b
}

// BARS builds a BARS 1.2 container (AMTA v5), little-endian, with entries
// sorted ascending by CRC-32 of their names.
func BARS(entries []Entry) []byte {
	n := len(entries)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return CRC(entries[order[a]].Name) < CRC(entries[order[b]].Name) })

	pos := 0x10 + 12*n
	amtaOff := make([]int, n) // by input index
	blocks := make([][]byte, n)
	for _, i := range order {
		blocks[i] = amtaBlock(entries[i])
		amtaOff[i] = pos
		pos += len(blocks[i])
	}
	bwavOff := make([]uint32, n)
	for i := range bwavOff {
		bwavOff[i] = 0xFFFFFFFF
	}
	var payload [][]byte
	var payloadOff []int
	pos = align(pos, 0x40)
	for _, i := range order {
		e := entries[i]
		if e.AliasOf != 0 || e.BWAV == nil {
			continue
		}
		bwavOff[i] = uint32(pos)
		payload = append(payload, e.BWAV)
		payloadOff = append(payloadOff, pos)
		pos = align(pos+len(e.BWAV), 0x40)
	}
	for i, e := range entries {
		if e.AliasOf != 0 {
			bwavOff[i] = bwavOff[e.AliasOf-1]
		}
	}
	d := make([]byte, pos)
	copy(d, "BARS")
	le.PutUint32(d[4:], uint32(pos))
	d[8], d[9] = 0xFF, 0xFE
	le.PutUint16(d[10:], 0x0102)
	le.PutUint32(d[12:], uint32(n))
	for k, i := range order {
		le.PutUint32(d[0x10+4*k:], CRC(entries[i].Name))
		le.PutUint32(d[0x10+4*n+8*k:], uint32(amtaOff[i]))
		le.PutUint32(d[0x10+4*n+8*k+4:], bwavOff[i])
		copy(d[amtaOff[i]:], blocks[i])
	}
	for k, p := range payload {
		copy(d[payloadOff[k]:], p)
	}
	return d
}

// NXOpus builds one mono "Switch Opus" stream (the per-channel payload of an
// Opus BWAV): a basic-info chunk, then a 0x80000004 data chunk holding each
// packet as u32be size, u32be final range (0), payload.
func NXOpus(packets [][]byte, preSkip uint16) []byte {
	var data []byte
	for _, p := range packets {
		data = binary.BigEndian.AppendUint32(data, uint32(len(p)))
		data = binary.BigEndian.AppendUint32(data, 0)
		data = append(data, p...)
	}
	d := make([]byte, 0x28, 0x28+len(data))
	le.PutUint32(d[0:], 0x80000001)
	le.PutUint32(d[4:], 0x18)
	d[9] = 1 // channels
	le.PutUint32(d[0xC:], 48000)
	le.PutUint32(d[0x10:], 0x20)
	le.PutUint16(d[0x1C:], preSkip)
	le.PutUint32(d[0x20:], 0x80000004)
	le.PutUint32(d[0x24:], uint32(len(data)))
	return append(d, data...)
}
