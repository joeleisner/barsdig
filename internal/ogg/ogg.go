// Package ogg writes Ogg Opus files (RFC 7845) from Opus packets, including
// multistream files with several uncoupled mono streams.
package ogg

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"barsdig/internal/bwav"
)

const (
	serial       = 0x62617273 // "bars"
	maxPagePkts  = 50
	flagCont     = 0x01
	flagBOS      = 0x02
	flagEOS      = 0x04
	vendor       = "barsdig"
	outputRate   = 48000
	maxFamily255 = 255
)

var crcTable = func() (t [256]uint32) {
	for i := range t {
		r := uint32(i) << 24
		for k := 0; k < 8; k++ {
			if r&0x80000000 != 0 {
				r = r<<1 ^ 0x04C11DB7
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return
}()

// PacketSamples returns the duration of an Opus packet in 48 kHz samples,
// from its TOC byte (RFC 6716 section 3.1).
func PacketSamples(pkt []byte) (int, error) {
	if len(pkt) == 0 {
		return 0, errors.New("ogg: empty Opus packet")
	}
	toc := pkt[0]
	cfg := toc >> 3
	var frame int
	switch {
	case cfg < 12:
		frame = [...]int{480, 960, 1920, 2880}[cfg&3]
	case cfg < 16:
		frame = [...]int{480, 960}[cfg&1]
	default:
		frame = [...]int{120, 240, 480, 960}[cfg&3]
	}
	switch toc & 3 {
	case 0:
		return frame, nil
	case 1, 2:
		return 2 * frame, nil
	}
	if len(pkt) < 2 {
		return 0, errors.New("ogg: code-3 Opus packet without a frame count")
	}
	return int(pkt[1]&0x3F) * frame, nil
}

// selfDelimit converts a single-frame (code 0) packet to the self-delimited
// form used for all but the last stream of a multistream packet.
func selfDelimit(pkt []byte) ([]byte, error) {
	if len(pkt) == 0 || pkt[0]&3 != 0 {
		return nil, errors.New("ogg: only single-frame (code 0) Opus packets can be multiplexed")
	}
	l := len(pkt) - 1
	out := make([]byte, 0, len(pkt)+2)
	out = append(out, pkt[0])
	if l < 252 {
		out = append(out, byte(l))
	} else {
		b1 := 252 + l&3
		out = append(out, byte(b1), byte((l-b1)>>2))
	}
	return append(out, pkt[1:]...), nil
}

// vorbisMapping maps Vorbis-order output channels to stream indices, assuming
// BWAV channels are in WAV order (L R C LFE BL BR); see docs/opus.md.
func vorbisMapping(n int) (family byte, mapping []byte, err error) {
	switch n {
	case 1:
		return 0, nil, nil
	case 2:
		return 1, []byte{0, 1}, nil
	case 3:
		return 1, []byte{0, 2, 1}, nil
	case 4:
		return 1, []byte{0, 1, 2, 3}, nil
	case 5:
		return 1, []byte{0, 2, 1, 3, 4}, nil
	case 6:
		return 1, []byte{0, 2, 1, 4, 5, 3}, nil
	}
	if n > maxFamily255 {
		return 0, nil, fmt.Errorf("ogg: %d channels is too many", n)
	}
	m := make([]byte, n)
	for i := range m {
		m[i] = byte(i)
	}
	return 255, m, nil
}

type pageWriter struct {
	w       io.Writer
	err     error
	seq     uint32
	lace    []byte
	body    []byte
	granule int64 // granule of the last packet completed on the pending page, or -1
	pkts    int
	cont    bool // the pending page continues a packet from the previous page
}

func (p *pageWriter) flush(flags byte, midPacket bool) {
	if p.err != nil {
		return
	}
	if p.cont {
		flags |= flagCont
	}
	hdr := make([]byte, 27, 27+len(p.lace)+len(p.body))
	copy(hdr, "OggS")
	hdr[5] = flags
	binary.LittleEndian.PutUint64(hdr[6:], uint64(p.granule))
	binary.LittleEndian.PutUint32(hdr[14:], serial)
	binary.LittleEndian.PutUint32(hdr[18:], p.seq)
	hdr[26] = byte(len(p.lace))
	page := append(append(hdr, p.lace...), p.body...)
	var crc uint32
	for _, b := range page {
		crc = crc<<8 ^ crcTable[byte(crc>>24)^b]
	}
	binary.LittleEndian.PutUint32(page[22:], crc)
	_, p.err = p.w.Write(page)
	p.seq++
	p.lace, p.body, p.granule, p.pkts, p.cont = p.lace[:0], p.body[:0], -1, 0, midPacket
}

// packet appends one packet; the page is flushed lazily so the last page is never empty.
func (p *pageWriter) packet(data []byte, granule int64) {
	if p.pkts >= maxPagePkts {
		p.flush(0, false)
	}
	rest := data
	started := false // true once part of this packet is on a page already
	for {
		if len(p.lace) == 255 {
			p.flush(0, started)
		}
		started = true
		n := len(rest)
		if n > 255 {
			n = 255
		}
		p.lace = append(p.lace, byte(n))
		p.body = append(p.body, rest[:n]...)
		rest = rest[n:]
		if n < 255 {
			break
		}
	}
	p.granule = granule
	p.pkts++
}

// WriteOpus writes the streams as one Ogg Opus file: a single stream as
// ordinary mono Opus, several as an uncoupled multistream (mapping family 1,
// or 255 above six channels). All streams must have the same packet count and
// all packets of a multistream must be single-frame (code 0).
func WriteOpus(w io.Writer, streams []bwav.OpusStream) error {
	if len(streams) == 0 {
		return errors.New("ogg: no streams")
	}
	n := len(streams)
	count := len(streams[0].Packets)
	for _, s := range streams {
		if len(s.Packets) != count {
			return errors.New("ogg: streams have different packet counts")
		}
	}
	family, mapping, err := vorbisMapping(n)
	if err != nil {
		return err
	}
	skip := streams[0].PreSkip
	limit := int64(skip) + int64(streams[0].Samples)

	// Build all audio packets first so errors surface before anything is written.
	audio := make([][]byte, count)
	grans := make([]int64, count)
	var total int64
	for k := 0; k < count; k++ {
		dur, err := PacketSamples(streams[0].Packets[k])
		if err != nil {
			return fmt.Errorf("ogg: packet %d: %w", k, err)
		}
		if n == 1 {
			audio[k] = streams[0].Packets[k]
		} else {
			var pk []byte
			for i, s := range streams {
				p := s.Packets[k]
				if i < n-1 {
					if p, err = selfDelimit(p); err != nil {
						return fmt.Errorf("ogg: packet %d stream %d: %w", k, i, err)
					}
				}
				pk = append(pk, p...)
			}
			audio[k] = pk
		}
		total += int64(dur)
		grans[k] = min(total, limit)
	}

	head := append([]byte("OpusHead"), 1, byte(n))
	head = binary.LittleEndian.AppendUint16(head, skip)
	head = binary.LittleEndian.AppendUint32(head, outputRate)
	head = binary.LittleEndian.AppendUint16(head, 0) // output gain
	head = append(head, family)
	if family != 0 {
		head = append(head, byte(n), 0)
		head = append(head, mapping...)
	}
	tags := append([]byte("OpusTags"), binary.LittleEndian.AppendUint32(nil, uint32(len(vendor)))...)
	tags = append(tags, vendor...)
	tags = binary.LittleEndian.AppendUint32(tags, 0)

	pw := &pageWriter{w: w, granule: -1}
	pw.granule = 0
	pw.packet(head, 0)
	pw.flush(flagBOS, false)
	pw.granule = 0
	pw.packet(tags, 0)
	pw.flush(0, false)
	for k, a := range audio {
		pw.packet(a, grans[k])
	}
	pw.flush(flagEOS, false)
	return pw.err
}
