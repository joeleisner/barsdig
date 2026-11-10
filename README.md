# barsdig

Dig through the audio in an extracted game RomFS: catalog every `BARS`/`BWAV` cue, search and audition them in a local web
page, mark what you want, and export it as `.wav` (or `.opus`) with a JSON sidecar. One static binary, no dependencies.

> **Not affiliated with Nintendo.** barsdig is an independent, unofficial tool. Nintendo, Nintendo Switch and any game names are
> trademarks of their respective owners and are mentioned only to describe the file formats the tool reads.
>
> This repository contains **no game data**: no audio, keys, firmware or copyrighted assets, and its tests use synthetic files.
> The tool does not decrypt, circumvent copy protection, or download anything. It reads files you have **already extracted from a
> copy of a game you own**. You are responsible for following the laws and licences that apply to those files. Please do not
> share extracted game audio.

## Install

Download the archive for your system from the [Releases](../../releases) page and unpack it.

    sha256sum -c checksums.txt --ignore-missing                  # verify the download
    gh attestation verify barsdig_*_linux_amd64.tar.gz -R <owner>/<repo>   # optional: verify build provenance

Binaries are unsigned. On macOS, clear the quarantine flag once (`xattr -d com.apple.quarantine ./barsdig`); on Windows,
SmartScreen may warn about an unrecognised app.

To build from source you need [mise](https://mise.jdx.dev) (or Go 1.27+):

    mise install
    mise run build        # ./barsdig, a static binary

## Use

Point it at an extracted RomFS folder (it reads `Sound/Resource` and `Voice/Resource`).

    barsdig serve  <romfs>                       # local web UI: search, listen, mark, export
    barsdig index  <romfs>                       # scan and print a summary and any problems
    barsdig export <romfs> --out DIR --match "footstep stone"
    barsdig export <romfs> --out DIR --picks picks.txt   # one cue ID per line, # comments allowed

Export layout: `<out>/<bank>/<cue>.wav` (DSP-ADPCM, PCM16) or `.opus` (Opus, remuxed to Ogg, not re-encoded), each with a
`<cue>.json` sidecar describing it. Original `.bwav` files are never written. Where a bank only holds a short prefetch clip,
the full file from `Stream/` is used.

`serve` listens on 127.0.0.1:8080 and refuses other addresses unless you pass `--allow-nonloopback`. The catalog is cached in
your user cache directory (`--cache-dir`), so after the first scan later starts take a fraction of a second.

Opus is played in the browser and exported as `.opus`; convert it with `ffmpeg` if you need WAV.

## Develop

    mise tasks            # list tasks
    mise run check        # vet, gofmt, tests
    mise run fuzz         # brief fuzzing of the parsers and muxer
    mise run cross        # cross-compile for Linux, macOS and Windows

`BARSDIG_TEST_ROMFS=/path/to/your/romfs mise run acceptance` runs extra checks against a RomFS of your own (nothing from it is
stored). Format notes: [docs/format.md](docs/format.md), [docs/opus.md](docs/opus.md).

## License

MIT, see [LICENSE](LICENSE). Third-party notices: [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).
