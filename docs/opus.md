# Opus in BWAV, and how barsdig writes Ogg Opus

## Where the Opus data is

An Opus-coded BWAV (codec id 2) holds one **independent mono "Switch Opus" stream per channel**, at each channel header's data
offset (`+0x34`). A stereo file is two streams, a 6-channel file is six.

```
+0x00 u32 0x80000001   basic info chunk
+0x04 u32 chunk size (0x18)
+0x09 u8  channels (1)
+0x0C u32 sample rate (48000)
+0x10 u32 data chunk offset (0x20), relative to the stream start
+0x1C u16 pre-skip (120)
at the data chunk:  u32 0x80000004 | u32 data size | packets
packet:             u32 big-endian size | u32 big-endian final range | Opus packet bytes
```

- Packets are plain Opus packets. In the files examined every packet had TOC byte `0xF8` (CELT fullband, 20 ms, one frame),
  so each is 960 samples at 48 kHz.
- All channels of a file have the same packet count.
- Zero-length packets can occur. barsdig replaces each with the 3-byte Opus silence packet `F8 FF FE`, which has the same
  duration, so the Ogg stays valid.
- The BWAV sample count and the packets' total (`packets x 960 - pre-skip`) can differ slightly in either direction. The final
  Ogg granule position is clipped to `min(total packet samples, pre-skip + BWAV samples)`.

## Ogg Opus output

- **One channel:** ordinary Ogg Opus, mapping family 0.
- **Two or more:** one Ogg Opus **multistream**: `OpusHead` mapping family 1 (family 255 above six channels),
  `stream_count = channels`, `coupled_count = 0`, with a channel-mapping table (below). Each Ogg packet is one packet from every
  stream, in stream order, all but the last in Opus *self-delimited* form. For a single-frame packet that is
  `TOC | length | frame` where `length` is one byte if below 252, otherwise two bytes `252 + (L & 3)`, `(L - b1) >> 2`
  (L = packet length minus the TOC byte). Only single-frame packets are supported.
- Pre-skip 120, input rate 48000, output gain 0, `OpusTags` with a vendor string and no comments.
- Page granule = total samples through the page's last packet (including pre-skip); the last page has the end-of-stream flag.
  Packets larger than one page continue on following pages (the continued flag is set only on a page that really continues a
  packet).
- Checked with `ffprobe`, `opusinfo` and `ffmpeg` for mono, stereo and 6-channel files.

### Channel mapping

BWAV channel order is assumed to be WAV order (L R C LFE BL BR). `mapping[output channel] = stream index`, output in Vorbis
order (FL FC FR BL BR LFE):

| channels | mapping |
|---|---|
| 2 | `[0, 1]` |
| 3 | `[0, 2, 1]` |
| 4 | `[0, 1, 2, 3]` |
| 5 | `[0, 2, 1, 3, 4]` |
| 6 | `[0, 2, 1, 4, 5, 3]` |

The assumption is unverified. Channel order is recorded in the sidecar JSON for files with more than two channels.
