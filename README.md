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
- ⏯️ Resume from breakpoint — missing segments are re-fetched and partially
  downloaded segments continue byte-by-byte via HTTP Range
- 🚦 Aggregate speed cap with `--rate-limit` (e.g. `2M`, `500KB`; over all
  download threads combined)
- 🌐 Shared HTTP client with HTTP/2 multiplexing and Range resume
- 🎞️ Prefers `ffmpeg` for lossless concat merge; built-in fallback included
- 🤖 Machine-friendly `--json` output; logs/progress go to stderr, results to stdout
- 📦 Batch downloads from a URL list file
- ✅ Old flag compatibility (`-ht`/`-sp`/`-pd`/`-ua`) for legacy scripts

---

## Installation

Download a pre-built binary from the [Releases page](../../releases) page, or
use one of the options below.

> 🤖 **For Agents:** you can install and drive this tool automatically. For a
> compact, machine-readable quickstart (install commands, flags, JSON output),
> read the [`llms.txt`](llms.txt) file in this repo — then simply run
> `m3u8dl --url <playlist-url> --json`.

### Option 0 — GitHub Release (pre-compiled binaries)

Pre-built binaries are available for **Linux / macOS / Windows** on
**amd64 / arm64**. They are published as archives: `.tar.gz` on Linux/macOS and
`.zip` on Windows. Archive names follow
`m3u8dl_<ver>_<OS>_<ARCH>.tar.gz|zip` where `<OS>` is `Linux` / `Darwin` /
`Windows` and amd64 is named `x86_64`. Download and unpack, e.g. `v1.0.0` on
Linux/amd64:

```bash
curl -fSL -o m3u8dl.tar.gz \
  "https://github.com/dingdayu/m3u8dl/releases/download/v1.0.0/m3u8dl_v1.0.0_Linux_x86_64.tar.gz"
tar -xzf m3u8dl.tar.gz
chmod +x m3u8dl
sudo mv m3u8dl /usr/local/bin/
m3u8dl --version
```

> **China / slow network?** GitHub downloads can be slow there. We recommend
> the **npm install** below (npmmirror mirrors it and jsDelivr can serve the
> binaries over CDN), or `go install` with `GOPROXY=https://goproxy.cn`. See
> the [**China download acceleration guide**](README.zh.md#国内加速下载).

### Option 0b — npm (Node.js, cross-platform)

`m3u8dl` is also published to **npm** as [`@dingdayu/m3u8dl`](https://www.npmjs.com/package/@dingdayu/m3u8dl),
a meta package that automatically selects the right pre-built binary for your
OS/arch:

```bash
# Global CLI install (puts `m3u8dl` on your PATH)
npm install -g @dingdayu/m3u8dl

# Or run once without installing
npx @dingdayu/m3u8dl --url https://example.com/index.m3u8 --json
```

Supported platforms: `darwin-arm64`, `darwin-x64`, `linux-arm64`,
`linux-x64`, `win32-x64`. Other platforms can use `go install` (below).

> **China / npmmirror:** the npm registry is fully mirrored by
> [npmmirror.com](https://npmmirror.com) (Alibaba), which is fast in China:
>
> ```bash
> npm install -g @dingdayu/m3u8dl --registry=https://registry.npmmirror.com
> ```
>
> The platform binaries inside the packages are also served by
> [jsDelivr](https://www.jsdelivr.com/) — a CDN backed by Cloudflare/Fastly —
> at `https://cdn.jsdelivr.net/npm/@dingdayu/m3u8dl-linux-x64@<version>/bin/m3u8dl`.

### Verifying downloads (recommended)

Release artifacts are **attested** by the build. Verification is simple:

1. **Checksums** — every release has `checksums.txt` (SHA-256 of the archives):

```bash
sha256sum -c checksums.txt --ignore-missing
```

2. **GitHub Artifact Attestations** — the release **archives** (`.tar.gz` /
   `.zip`) are attested on GitHub; a single command confirms they were built by
   this repo's release workflow:

```bash
gh attestation verify m3u8dl_v1.0.0_Linux_x86_64.tar.gz --repo dingdayu/m3u8dl
```

3. **Standalone binaries (npm / jsDelivr)** — a bare binary downloaded from
   jsDelivr (or unpacked from npm) is not an attested artifact. Verify it
   against `bin-checksums.txt`, which is published on the release and itself
   attested:

```bash
curl -fSLO https://github.com/dingdayu/m3u8dl/releases/download/v1.0.0/bin-checksums.txt
sha256sum -c bin-checksums.txt --ignore-missing
# Optional: prove the manifest itself came from this repo's release build:
gh attestation verify bin-checksums.txt --repo dingdayu/m3u8dl
```

4. **npm provenance & self-check** — npm packages are published with
   [provenance](https://docs.npmjs.com/generating-provenance-statements)
   (see the badge on the package page, or `npm audit signatures`). In addition,
   the `m3u8dl` launcher verifies the platform binary's SHA-256 against the
   digest recorded at pack time **on every run**, and aborts with a clear
   error if the download was corrupted or tampered with:

```bash
npm audit signatures
```

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

# Cap the aggregate download speed at ~2 MiB/s
m3u8dl -u https://example.com/index.m3u8 --rate-limit 2M

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
      --rate-limit string aggregate download speed cap, e.g. 2M / 500KB /
                          200000 (bytes/s; 0 or empty = unlimited)
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

### Speed limit

`--rate-limit` caps the **aggregate** download speed across all threads with a
token bucket:

```bash
m3u8dl -u https://example.com/index.m3u8 --rate-limit 2M   # ≈2 MiB/s total
m3u8dl -u https://example.com/index.m3u8 --rate-limit 500KB
m3u8dl -u https://example.com/index.m3u8 --rate-limit 200000  # plain bytes/s
```

Suffixes `K/KB`, `M/MB`, `G/GB` are 1024-based and case-insensitive; `0` or
empty (the default) means unlimited.

### Resume, Range & HTTP/2

Downloads share one pooled `http.Client`, and TLS requests negotiate
**HTTP/2** (multiplexed over a single connection) even with `--insecure`.
Every interrupted segment is kept as `<name>.ts.part`; the next retry — or the
next run of the same command — resumes it mid-file with
`Range: bytes=<n>-`:

- `206` whose range matches the existing bytes → the tail is appended
- `200` (server ignores Range) → the segment restarts from scratch
- `416` (stale offset, e.g. the resource shrank) → the `.part` is dropped and
  the segment is re-downloaded whole
- `auto` host fallback always restarts clean, since the alternate URL may be a
  different resource

### JSON output

With `--json`, all results are printed to **stdout** as a single JSON object:

```json
{
  "ok": true,
  "path": "/data/videos/movie.mp4",
  "duration_sec": 12.34,
  "url": "https://...",
  "name": "movie",
  "mode": "single"
}
```

On failure:

```json
{ "ok": false, "error": "download failed ...", "mode": "single" }
```

Logs, progress and warnings go to **stderr**, so you can safely parse only the
stdout JSON.

---

## Agent Skills

This repository ships an [Agent Skill](https://agentskills.io) — a
`SKILL.md` folder that teaches AI coding agents (VS Code / GitHub Copilot,
Claude Code, Codex, Gemini CLI, OpenCode, …) how to install and drive
`m3u8dl` correctly: the `--json` contract, resume behavior, every flag, and
troubleshooting. Compatible agents **discover it automatically** when the
repo is open, and can load it on demand or via `/m3u8dl` in chat.

Every released binary also **embeds the skill**, so an installed `m3u8dl` can
register it for you:

```bash
m3u8dl skills list                       # what the binary bundles
m3u8dl skills install                    # -> ./.agents/skills/m3u8dl/
m3u8dl skills install --agent claude     # -> ./.claude/skills/m3u8dl/
m3u8dl skills install --scope user       # -> ~/.agents/skills/ (all projects)
```

Sources: canonical skill in
[`.agents/skills/m3u8dl/`](.agents/skills/m3u8dl/SKILL.md); the
`.claude/skills/` mirror is generated (`make skills-sync`). See
[`AGENTS.md`](AGENTS.md#agent-skills-for-users-coding-agents) for authoring
conventions.

---

## Shell Completion

`m3u8dl` uses cobra's built-in `completion` subcommand to generate
auto-completion scripts for **bash**, **zsh**, **fish** and **powershell**.
Run `m3u8dl completion <shell> --help` for shell-specific notes.

Load completions for the **current session only**:

```bash
source <(m3u8dl completion bash)    # bash  (needs the bash-completion package)
source <(m3u8dl completion zsh)     # zsh
m3u8dl completion fish | source     # fish
m3u8dl completion powershell | Out-String | Invoke-Expression  # PowerShell
```

Install them **persistently** (then open a new shell):

```bash
# bash — Linux (may need sudo); macOS: $(brew --prefix)/etc/bash_completion.d/m3u8dl
m3u8dl completion bash > /etc/bash_completion.d/m3u8dl

# zsh — Linux; macOS: $(brew --prefix)/share/zsh/site-functions/_m3u8dl
#   (zsh completion must already be enabled: autoload -U compinit; compinit)
m3u8dl completion zsh > "${fpath[1]}/_m3u8dl"

# fish
m3u8dl completion fish > ~/.config/fish/completions/m3u8dl.fish

# PowerShell — append the line below to your $PROFILE
m3u8dl completion powershell | Out-String | Invoke-Expression
```

---

## Examples

### Resume a failed download

If some segments fail, the ts directory is kept — and so is each segment's
byte-level progress in a `<name>.ts.part` file. Re-run the same command: only
the missing segments are re-fetched, and partially downloaded ones continue
where they stopped via HTTP Range (see [Resume, Range &
HTTP/2](#resume-range--http2)).

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
make install-hooks   # install git hooks via lefthook
make release     # run goreleaser (requires goreleaser installed)
```

### Git hooks & editor-agnostic style

This repo uses [Lefthook](https://lefthook.dev) (a single Go binary) to manage
git hooks, ensuring consistent code style regardless of editor/OS:

- `make install-hooks` → installs lefthook and sets up `pre-commit` + `commit-msg` hooks
  - `pre-commit` — checks/fixes `gofmt`, `go vet`, EOF newline,
    trailing whitespace, LF line endings, YAML/JSON validation, large-file checks
  - `commit-msg` — enforces [Conventional Commits](CONTRIBUTING.md#commit-messages)
- `lefthook.yml` — hook configuration (parallel execution, auto-stage fixes)
- `.gitattributes` — forces LF, declares text/binary, helps GitHub Linguist
- `.editorconfig` — editor-agnostic indent/encoding/eol defaults

---

## Roadmap

- [x] Add CLI shell completion scripts/docs (see [Shell
      Completion](#shell-completion))
- [ ] Publish a Homebrew tap
- [x] Add end-to-end tests with a mock m3u8 server (`m3u8dl_test.go`,
      `httptest`-based segment/transport suite)
- [x] Support HTTP/2, range requests, and speed limits (`--rate-limit`,
      byte-level `.part` resume)

---

## License

[MIT](LICENSE) © [dingdayu](https://github.com/dingdayu)

---

🌐 中文文档见 [README.zh.md](README.zh.md)
