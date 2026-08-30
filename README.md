# m3u8dl

> 🌐 <b>Language</b>: **English** | [**简体中文**](README.zh.md)

A fast, multi-threaded **m3u8 video downloader** written in Go.

It parses an m3u8 playlist, downloads all TS segments concurrently, and merges
them into a single MP4 — supporting **AES decryption**, **nested m3u8
(Master Playlist)**, **ad/duplicate segment removal**, and **resume from
breakpoint**. It prefers system `ffmpeg` for a lossless merge and falls back to
a built-in merge when `ffmpeg` is unavailable.

The CLI is built with [cobra](https://github.com/spf13/cobra): semantic long
flags with short aliases, plus `--help` / `--json` / `--version` and shell
auto-completion. It is friendly to both humans and LLM/Agent workflows.

---

## Features

- ⚡ Multi-threaded TS segment download with a live progress bar and speed meter
- 🔐 AES-128 CBC decryption (per-segment keys), with IV reuse fallback
- 🪆 Nested m3u8 / Master Playlist expansion (recursive)
- 🛡️ Ad / duplicate segment deduplication by content hash (MD5)
- ⏯️ Resume from breakpoint — re-run the same command to fetch the missing ones
- 🎞️ Prefers `ffmpeg` for lossless concat merge; built-in fallback included
- 🤖 Machine-friendly `--json` output; logs/progress go to stderr, results to stdout
- 📦 Batch downloads from a URL list file
- ✅ Old flag compatibility (`-ht`/`-sp`/`-pd`/`-ua`) for legacy scripts

---

## Installation

Download a pre-built binary from the [Releases page](../../releases) page, or
use one of the options below.

### Option 0 — GitHub Release (pre-compiled binaries)

Pre-built binaries are available for **Linux / macOS / Windows** on
**amd64 / arm64**. Download and unpack, e.g. `v1.0.0` on Linux/amd64:

```bash
curl -fSL -o m3u8dl.tar.gz \
    "https://github.com/dingdayu/m3u8dl/releases/download/v1.0.0/m3u8dl_v1.0.0_linux_amd64.tar.gz"
tar -xzf m3u8dl.tar.gz
chmod +x m3u8dl
sudo mv m3u8dl /usr/local/bin/
m3u8dl --version
```

> **China / slow network?** GitHub downloads can be slow there. Use a trusted
> mirror prefix for the same URL (e.g. `https://ghfast.top/`) or a one-click
> install script. See the
> [**China download acceleration guide**](README.zh.md#国内加速下载) for details.

### Option A — `go install` (requires Go 1.27+)

```bash
go install github.com/dingdayu/m3u8dl@latest
```

### Option B — build from source

```bash
git clone https://github.com/dingdayu/m3u8dl.git
cd m3u8dl
make build          # produces ./bin/m3u8dl
```

### Option C — Homebrew (when a tap is published)

```bash
# brew install dingdayu/tap/m3u8dl
```

---

## Quick Start

```bash
# Download a single m3u8
m3u8dl -u https://example.com/index.m3u8

# Custom output name and 32 threads
m3u8dl -u https://example.com/index.m3u8 -o myvideo -n 32

# Structure the JSON output (great for scripts/LLM/Agent)
m3u8dl --url https://example.com/index.m3u8 --threads 16 --json

# Batch download
m3u8dl --list urls.txt --save-path /data/videos
```

---

## Usage

```
m3u8dl [flags] <m3u8-url> [outputName]

Flags:
  -u, --url string        m3u8 URL (http(s)://url/xx/index.m3u8)
  -n, --threads int       number of download threads (default 24)
      --host-type string  host resolution (v1|v2|auto) [legacy -ht] (default "v2")
  -o, --output string     output filename without extension (default "movie")
  -c, --cookie string     request Cookie
  -r, --referer string    Referer header (defaults to the m3u8 host)
  -t, --timeout int       per-request timeout in seconds (default 120)
      --save-path string  save directory [legacy -sp]
      --user-agent string User-Agent header [legacy -ua]
  -s, --insecure          allow insecure TLS requests
      --purge-dup         remove ad/duplicate segments [legacy -pd]
      --clean-ts          delete the ts directory after a successful merge (default true)
  -j, --json              output structured JSON
  -l, --list string       batch download list file (one m3u8 URL per line)
  -H, --header strings    custom request header, repeatable ("Key: Value")
  -h, --help              help for m3u8dl
      --version           version for m3u8dl
```

### Host type

Some servers resolve relative TS paths differently. `--host-type` lets you
choose the resolution strategy:

- `v2` (default) — resolve TS against `scheme://host`
- `v1` — resolve TS against `scheme://host/dir-of-m3u8`
- `auto` — use `v1` and fall back to `v2` on download failure

### JSON output

With `--json`, all results are printed to **stdout** as a single JSON object:

```json
{"ok":true,"path":"/data/videos/movie.mp4","duration_sec":12.34,"url":"https://...","name":"movie","mode":"single"}
```

On failure:

```json
{"ok":false,"error":"download failed ...","mode":"single"}
```

Logs, progress and warnings go to **stderr**, so you can safely parse only the
stdout JSON.

---

## Examples

### Resume a failed download

If some segments fail, the ts directory is kept. Simply re-run the same command
and only the missing segments are re-fetched.

### Remove ad segments

```bash
m3u8dl -u https://example.com/index.m3u8 --purge-dup
```

### Download from an encrypted playlist

Encryption keys (AES-128) are detected from `#EXT-X-KEY` automatically. If the
key has no explicit IV, the first 16 bytes of the key are used as IV.

---

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines, and
[AGENTS.md](AGENTS.md) for Agent-friendly onboarding.

```bash
make build       # build to ./bin/m3u8dl
make test        # run tests
make lint        # run golangci-lint
make install-hooks   # install git hooks (style/format/conventional-commits)
make release     # run goreleaser (requires goreleaser installed)
```

### Git hooks & editor-agnostic style

This repo ships commit hooks and style tooling so the codebase stays clean no
matter which editor/OS contributors use (EOF newlines, trailing whitespace,
mixed line endings, Go formatting, commit-message conventions):

- `make install-hooks` → sets `core.hooksPath` to `.hooks/`
    - `.hooks/pre-commit` — checks/fixes `gofmt`, `go vet`, EOF newline,
        trailing whitespace, LF line endings
    - `.hooks/commit-msg` — enforces [Conventional Commits](CONTRIBUTING.md#commit-messages)
- Optional `pre-commit` framework (`.pre-commit-config.yaml`) with
    `end-of-file-fixer`, `trailing-whitespace`, `check-yaml`, `check-json`, …
- `.gitattributes` — forces LF, declares text/binary, helps GitHub Linguist
- `.editorconfig` — editor-agnostic indent/encoding/eol defaults

---

## Roadmap

- [ ] Add CLI shell completion scripts/docs
- [ ] Publish a Homebrew tap
- [ ] Add end-to-end tests with a mock m3u8 server
- [ ] Support HTTP/2, range requests, and speed limits

---

## License

[MIT](LICENSE) © [dingdayu](https://github.com/dingdayu)

---

🌐 中文文档见 [README.zh.md](README.zh.md)
