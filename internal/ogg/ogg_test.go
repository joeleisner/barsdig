package ogg

import (
	"bytes"
	"encoding/binary"
	"testing"

	"barsdig/internal/bwav"
)

var le = binary.LittleEndian

type page struct {
	flags   byte
	granule int64
	lace    []byte
	body    []byte
}

func crcOf(b []byte) uint32 {
	var c uint32
	for _, x := range b {
		c = c<<8 ^ crcTable[byte(c>>24)^x]
	}
	return c
}

// parsePages verifies capture pattern, CRC and sequence numbers.
func parsePages(t *testing.T, d []byte) []page {
	t.Helper()
	var pages []page
	seq := uint32(0)
	for p := 0; p < len(d); {
		if string(d[p:p+4]) != "OggS" || d[p+4] != 0 {
			t.Fatalf("bad capture pattern at %d", p)
		}
		n := int(d[p+26])
		lace := d[p+27 : p+27+n]
		size := 0
		for _, l := range lace {
			size += int(l)
		}
		end := p + 27 + n + size
		cp := append([]byte(nil), d[p:end]...)
		stored := le.Uint32(cp[22:])
		le.PutUint32(cp[22:], 0)
		if crcOf(cp) != stored {
			t.Fatalf("page at %d: bad CRC", p)
		}
		if le.Uint32(d[p+18:]) != seq {
			t.Fatalf("page at %d: sequence %d, want %d", p, le.Uint32(d[p+18:]), seq)
		}
		seq++
		pages = append(pages, page{d[p+5], int64(le.Uint64(d[p+6:])), lace, d[p+27+n : end]})
		p = end
	}
	return pages
}

// packets reassembles complete packets (including continuation across pages).
func packets(pages []page) [][]byte {
	var out [][]byte
	var cur []byte
	for _, pg := range pages {
		off := 0
		for _, l := range pg.lace {
			cur = append(cur, pg.body[off:off+int(l)]...)
			off += int(l)
			if l < 255 {
				out = append(out, cur)
				cur = nil
			}
		}
	}
	return out
}

func stream(ch int, packets ...[]byte) bwav.OpusStream {
	return bwav.OpusStream{Channel: ch, PreSkip: 120, Samples: 1_000_000, Packets: packets}
}

var sil = []byte{0xF8, 0xFF, 0xFE}

func TestPacketSamples(t *testing.T) {
	cases := []struct {
		pkt  []byte
		want int
	}{
		{[]byte{0x00}, 480},        // SILK NB 10 ms
		{[]byte{0x08}, 960},        // SILK NB 20 ms
		{[]byte{0xF8}, 960},        // CELT FB 20 ms
		{[]byte{0xF0}, 480},        // CELT FB 10 ms
		{[]byte{0xE8}, 240},        // CELT 5 ms
		{[]byte{0x70}, 480},        // hybrid SWB 10 ms (config 14)
		{[]byte{0xF9}, 1920},       // code 1: two frames
		{[]byte{0xFB, 0x03}, 2880}, // code 3: three frames
	}
	for _, c := range cases {
		got, err := PacketSamples(c.pkt)
		if err != nil || got != c.want {
			t.Errorf("PacketSamples(% x) = %d, %v; want %d", c.pkt, got, err, c.want)
		}
	}
	for _, bad := range [][]byte{{}, {0xFB}} {
		if _, err := PacketSamples(bad); err == nil {
			t.Errorf("PacketSamples(% x) accepted", bad)
		}
	}
}

func TestPagesWellFormedMono(t *testing.T) {
	s := stream(0, sil, sil, sil, sil, sil)
	s.Samples = 4600 // final granule = min(5*960, 120+4600) = 4720
	var b bytes.Buffer
	if err := WriteOpus(&b, []bwav.OpusStream{s}); err != nil {
		t.Fatal(err)
	}
	pages := parsePages(t, b.Bytes())
	if len(pages) < 3 {
		t.Fatalf("%d pages", len(pages))
	}
	head := pages[0].body
	want := append([]byte("OpusHead"), 1, 1, 120, 0, 0x80, 0xBB, 0, 0, 0, 0, 0)
	if pages[0].flags != 0x02 || !bytes.Equal(head, want) {
		t.Fatalf("head page flags %#x body % x", pages[0].flags, head)
	}
	if !bytes.HasPrefix(pages[1].body, []byte("OpusTags")) {
		t.Fatalf("second page = %q", pages[1].body)
	}
	last := pages[len(pages)-1]
	if last.flags&0x04 == 0 || last.granule != 4720 {
		t.Fatalf("last page flags %#x granule %d", last.flags, last.granule)
	}
	pk := packets(pages)[2:]
	if len(pk) != 5 {
		t.Fatalf("%d audio packets", len(pk))
	}
	for _, p := range pk {
		if !bytes.Equal(p, sil) {
			t.Fatalf("packet % x", p)
		}
	}
}

func TestStereoMultistreamSelfDelimited(t *testing.T) {
	var b bytes.Buffer
	if err := WriteOpus(&b, []bwav.OpusStream{stream(0, sil), stream(1, sil)}); err != nil {
		t.Fatal(err)
	}
	pk := packets(parsePages(t, b.Bytes()))
	h := pk[0]
	if h[8] != 1 || h[9] != 2 || h[18] != 1 || h[19] != 2 || h[20] != 0 || h[21] != 0 || h[22] != 1 {
		t.Fatalf("OpusHead = % x (want family 1, 2 streams, 0 coupled, map 0 1)", h)
	}
	want := []byte{0xF8, 0x02, 0xFF, 0xFE, 0xF8, 0xFF, 0xFE} // self-delimited first stream, raw last
	if !bytes.Equal(pk[2], want) {
		t.Fatalf("audio packet = % x, want % x", pk[2], want)
	}
}

func TestSixChannelMapping(t *testing.T) {
	var ss []bwav.OpusStream
	for i := 0; i < 6; i++ {
		ss = append(ss, stream(i, sil))
	}
	var b bytes.Buffer
	if err := WriteOpus(&b, ss); err != nil {
		t.Fatal(err)
	}
	h := packets(parsePages(t, b.Bytes()))[0]
	if h[9] != 6 || h[18] != 1 || h[19] != 6 || h[20] != 0 || !bytes.Equal(h[21:27], []byte{0, 2, 1, 4, 5, 3}) {
		t.Fatalf("OpusHead = % x", h)
	}
}

func TestSelfDelimitTwoByteLength(t *testing.T) {
	pkt := append([]byte{0xF8}, bytes.Repeat([]byte{7}, 299)...) // frame length 299
	got, err := selfDelimit(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 0xF8 || got[1] != 255 || got[2] != 11 || len(got) != len(pkt)+2 { // 255 + 4*11 = 299
		t.Fatalf("header % x", got[:4])
	}
	if _, err := selfDelimit([]byte{0xF9, 1}); err == nil {
		t.Fatal("non code-0 packet accepted")
	}
}

func TestLargePacketsSpanPages(t *testing.T) {
	big := append([]byte{0xF8}, bytes.Repeat([]byte{0xAA}, 70000)...)
	var b bytes.Buffer
	if err := WriteOpus(&b, []bwav.OpusStream{stream(0, big, sil)}); err != nil {
		t.Fatal(err)
	}
	pages := parsePages(t, b.Bytes())
	pk := packets(pages)
	if len(pk) != 4 || !bytes.Equal(pk[2], big) || !bytes.Equal(pk[3], sil) {
		t.Fatalf("%d packets; big packet intact = %v", len(pk), len(pk) > 2 && bytes.Equal(pk[2], big))
	}
	audio := pages[2:]
	if len(audio) < 2 || audio[0].granule != -1 || audio[1].flags&0x01 == 0 {
		t.Fatalf("audio pages: first granule %d, second flags %#x", audio[0].granule, audio[1].flags)
	}
}

func TestWriteOpusRejectsBadInput(t *testing.T) {
	var b bytes.Buffer
	if err := WriteOpus(&b, nil); err == nil {
		t.Error("no streams accepted")
	}
	if err := WriteOpus(&b, []bwav.OpusStream{stream(0, sil, sil), stream(1, sil)}); err == nil {
		t.Error("unequal packet counts accepted")
	}
	if err := WriteOpus(&b, []bwav.OpusStream{stream(0, []byte{0xF9, 1, 2}), stream(1, []byte{0xF9, 1, 2})}); err == nil {
		t.Error("non code-0 multistream packet accepted")
	}
	if err := WriteOpus(&b, []bwav.OpusStream{stream(0, []byte{})}); err == nil {
		t.Error("empty packet accepted")
	}
}

// A page that starts a fresh packet must not carry the "continued packet" flag,
// even when the previous page's lacing table was filled to exactly 255 entries.
func TestContinuedFlagOnlyWhenPacketContinues(t *testing.T) {
	// 3600-byte packets take 15 lacing segments; 17 of them fill a page's 255 entries exactly.
	pkt := append([]byte{0xF8}, bytes.Repeat([]byte{0x55}, 3599)...)
	var ps [][]byte
	for i := 0; i < 20; i++ {
		ps = append(ps, pkt)
	}
	var b bytes.Buffer
	if err := WriteOpus(&b, []bwav.OpusStream{stream(0, ps...)}); err != nil {
		t.Fatal(err)
	}
	pages := parsePages(t, b.Bytes())
	for i := 1; i < len(pages); i++ {
		prevEndsMidPacket := pages[i-1].lace[len(pages[i-1].lace)-1] == 255
		if got := pages[i].flags&0x01 != 0; got != prevEndsMidPacket {
			t.Errorf("page %d: continued flag = %v, but previous page ends mid-packet = %v", i, got, prevEndsMidPacket)
		}
	}
	for i, p := range packets(pages)[2:] {
		if !bytes.Equal(p, pkt) {
			t.Fatalf("packet %d corrupted", i)
		}
	}
}
