---
name: m3u8dl
description: "Download m3u8 / HLS videos to MP4 with the m3u8dl CLI. Use when the user asks to download an .m3u8 or HLS stream URL, save a video from a streaming link, decrypt AES-encrypted m3u8 segments, strip ad/duplicate HLS segments, resume a failed segment download, batch-download a list of m3u8 URLs, merge TS segments into MP4, or install/verify the m3u8dl binary. Triggers: m3u8, HLS, stream download, video downloader, ts segments, master playlist, m3u8dl."
license: MIT
metadata:
  author: dingdayu
  repository: https://github.com/dingdayu/m3u8dl
  version: "1.0"
---

# Downloading videos with m3u8dl

`m3u8dl` is a multi-threaded m3u8/HLS downloader: it fetches all TS segments of
a playlist concurrently, decrypts AES-encrypted segments, optionally strips
ad/duplicate segments, and merges everything into one `.mp4` via system
`ffmpeg` (with a built-in merge fallback). This skill drives the CLI on the
user's behalf.

## When to use

- The user gives an `.m3u8` / HLS URL and wants the video saved as MP4.
- Batch downloads from a list file.
- Recovering a partially failed download (resume).
- Installing, verifying, or updating the `m3u8dl` tool itself.

## Step 1 — make sure the tool exists

Run `m3u8dl --version`. If not found, follow
[references/install.md](references/install.md) (pick a channel: npm, `go
install`, release binary, or China mirrors), install it, and re-check the
version before continuing.

## Step 2 — run the download with `--json`

Always add `--json` so the result is machine-readable. **stdout carries exactly
one JSON object (the result); all logs and progress go to stderr — parse only
stdout.**

```bash
m3u8dl --url "https://example.com/index.m3u8" --json
# {"ok":true,"path":"/abs/path/movie.mp4","duration_sec":12.34,"mode":"single","url":"...","name":"movie"}
```

Default flags are sane (24 threads, output `movie.mp4` in the current
directory). Override only what the user asks for:

| Need                                  | Flag                                                                    |
| ------------------------------------- | ----------------------------------------------------------------------- |
| Output file base name (no extension)  | `-o <name>`                                                             |
| Output directory                      | `--save-path <dir>`                                                     |
| Concurrency (default 24)              | `-n <threads>`                                                          |
| Remove ad/duplicate segments          | `--purge-dup`                                                           |
| Batch: one URL per line               | `-l/--list <file.txt>`                                                  |
| Auth headers when the host needs them | `-c/--cookie`, `-r/--referer`, `-H "K: V"` (repeatable), `--user-agent` |
| Self-signed TLS                       | `-s/--insecure`                                                         |
| Per-request timeout seconds           | `-t <sec>`                                                              |

Full flag semantics: [references/flags.md](references/flags.md).

## Step 3 — interpret the result

Success = exit code `0` AND `"ok":true` on stdout. Report `path` and
`duration_sec` to the user. The JSON schema and every failure mode:
[references/json-output.md](references/json-output.md).

On failure: read `error` (and stderr logs), then consult
[references/troubleshooting.md](references/troubleshooting.md). Common ones:

- **Missing/broken segments** — the TS directory is kept; simply re-run the
  _same command_ to resume only the missing segments.
- **Encrypted playlist** — AES-128 is handled automatically when the `KEY` URI
  is reachable; 403s on segments usually mean the playlist host requires a
  cookie/referer (see flags above).
- **ffmpeg missing** — merge falls back to a built-in concatenator; prefer
  installing ffmpeg for lossless MP4 output.

## Gotchas

- Quote URLs — many contain `?`/`&` query strings that the shell would split.
- `--save-path` must already exist; `mkdir -p` it first.
- In batch mode (`--list`), a single failed URL fails the whole run
  (exit 1, `ok:false`); the log lists the failures. Re-running skips URLs
  whose MP4 already exists and retries the rest.
- `--purge-dup` removes _all_ occurrences of a duplicated segment hash; for
  legitimate playlists with repeated segments (looping backgrounds), leave it
  off.
- The positional form `m3u8dl <url> [name]` is legacy-compatible; prefer `-u`.
- Never claim success from exit code alone — always verify `ok:true`, since
  cobra errors also print a JSON object with `ok:false` to stdout.

## Safety

- Only download streams the user has the right to access; respect the target
  site's terms of service.
- Install only from the official channels in
  [references/install.md](references/install.md) and verify release checksums
  when the user's environment warrants it.
- Use moderate thread counts against non-corporate hosts (default 24 is
  already aggressive; drop to 8 on flaky servers).
