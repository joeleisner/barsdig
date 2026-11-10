# BARS 1.2, AMTA v5 and BWAV: format notes

Notes on the container formats barsdig reads, written from observation of files from games that use them. Facts here are
about file structure (field offsets and meanings) only. All integers are little-endian. **Verified** means barsdig's
acceptance test checks it on every file of a real RomFS; **unverified** means it comes from other people's documentation or has
not been checked.

The public Nintendo-File-Formats documentation (nintendo-formats.com) covers BARS 0x0101 and AMTA v1/3/4; this describes the
newer BARS 1.2 / AMTA 5.0 combination.

## Files

Many RomFS files are individually zstd-compressed by the game (`*.bars.zs`). Decompress first.

## BARS 1.2 container

| Offset | Size | Field |
|---|---|---|
| 0x00 | 4 | `"BARS"` |
| 0x04 | 4 | file size |
| 0x08 | 2 | byte-order mark, bytes `FF FE` (little-endian) |
| 0x0A | 2 | version `0x0102` |
| 0x0C | 4 | entry count N |
| 0x10 | 4N | CRC-32/IEEE of each entry's name, sorted ascending (verified) |
| 0x10+4N | 8N | per entry `(u32 amta_offset, u32 bwav_offset)`, parallel to the hash table, absolute offsets |
| ... | | AMTA blocks, then BWAV blocks aligned to 0x40 |

- `bwav_offset` is `0xFFFFFFFF` (or 0) when the entry has no audio.
- Several entries can share one `bwav_offset` (**aliases**). A BWAV block runs from its offset to the next higher offset, or to
  the end of the file for the last one. Aliases are not empty files.
- The hash table order is the lookup order, not the authoring order.

## AMTA v5 (audio metadata), one per entry

| Offset | Size | Field |
|---|---|---|
| 0x00 | 4 | `"AMTA"` |
| 0x04 | 2 | BOM `FF FE` |
| 0x06 | 2 | version `0x0500` |
| 0x08 | 4 | block size |
| 0x0C | 4 | reserved (0) |
| 0x10 | 4 | DATA offset (block-relative; 0x34 observed) |
| 0x14 | 4 | MARK offset, 0 if none |
| 0x18 | 4 | MINF offset, 0 if none. Not decoded. |
| 0x1C | 4 | "tag" offset, 0 if none. Not decoded. |
| 0x20 | 4 | reserved |
| 0x24 | n | DATA blob. Its first u32 is the blob's own size (0x28 observed). |

DATA blob (partly understood): `+0x00` size, `+0x04` CRC-32 of the name (same as the container hash table), channel count at
block offset 0x31, f32 volume in dB at `DATA offset + 0x0C` (block offset 0x40). The rest is unexplained; barsdig keeps it as raw
bytes.

**The cue name is a NUL-terminated string immediately after the blob, at `block + 0x24 + blob size`** (verified: it hashes to the
stored CRC-32 for every entry).

**MARK table** (at the MARK offset): u32 count, then 16-byte records `{u32 id, u32 name_offset, u32 start, u32 length}`. The name
offset is relative to the position of the `name_offset` field itself; names are NUL-terminated. `start` and `length` are
presumably samples (unverified).

## BWAV

```
0x00 "BWAV"   0x04 BOM FF FE   0x06 u16 version (1)   0x08 u32 (CRC-32 of the data, unverified)
0x0C u16 prefetch flag (1 = prefetch)   0x0E u16 channel count
then per channel (0x4C bytes each, from 0x10):
  +0x00 u16 codec (0 PCM16, 1 DSP-ADPCM, 2 Opus)   +0x02 u16 pan/layout (0 L, 1 R, 2 C; others unverified)
  +0x04 u32 sample rate   +0x08 u32 samples of the full sound   +0x0C u32 samples in this file
  +0x10 16 x s16 DSP coefficients   +0x34 u32 data offset   +0x3C s32 loop end (-1 = no loop)   +0x40 s32 loop start
  +0x44 s16 predictor, +0x46 s16 history 1, +0x48 s16 history 2
channel data blocks follow, each aligned to 0x40
```

- **DSP-ADPCM** (verified bit-exact against vgmstream): 8-byte frames of 14 samples; header byte = coefficient pair (high
  nibble) and scale exponent (low nibble). The last frame can be partial: a header byte plus one byte per two samples, with no
  padding to 8 bytes.
- **Loop**: `loop end` is exclusive.
- **Opus** (codec 2): each channel's data is an independent mono "Switch Opus" stream; see `docs/opus.md`.

### Prefetch stubs and header-only dummies

An embedded BWAV can hold less than the full sound:

- **Prefetch stubs** (flag set): only the first 0x3800 = 14,336 samples. The full file is a standalone BWAV named after the cue
  in a `Stream/` folder next to the banks.
- **Header-only dummies** (some banks): flag not set, "samples in this file" is 0, "samples of the full sound" is the real
  count, and the data is absent. The audio is again the standalone `Stream/` file.

barsdig treats both as stubs, plays and exports the full stream when one exists, and marks the cue as having no audio when
neither exists.

## Not verified

The meaning of BWAV pan ids above 2 (so the channel order of 3 to 6 channel files is assumed to be L R C LFE BL BR), the BWAV
header CRC at 0x08, the units of MARK `start`/`length`, and everything in the MINF and tag sections and the unexplained part of
the DATA blob.

## Sources and prior art

barsdig's code is original and MIT-licensed. The layouts above were worked out by inspecting real files and then checked
against them (names against their stored CRC-32, decoded audio against vgmstream). Other people's documentation and tools were
read along the way and are worth crediting:

- [Nintendo-File-Formats](https://nintendo-formats.com) documents BARS 0x0101 and AMTA v1/3/4.
- [vgmstream](https://github.com/vgmstream/vgmstream) (ISC-style licence) documents the BWAV and Switch Opus layouts.
- Other BARS/AMTA tools, for comparison of field layouts: BfAudioSharp (AGPL-3.0), NintendAUX, BARSReaderGUI, BARcSharp
  (Unlicense) and bars-to-bwav (MIT). No code was copied from any of them.
