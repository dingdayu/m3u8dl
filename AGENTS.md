# AGENTS.md

Guidance for coding agents (and humans who want to use this project) working in
the **m3u8dl** repository. Read this before making changes or automating
downloads with the tool.

## Project Overview

`m3u8dl` is a multi-threaded m3u8 video downloader written in Go. It downloads
all TS segments of an m3u8 playlist concurrently, decrypts AES-encrypted
segments when needed, optionally strips ad/duplicate segments, and merges
everything into a single `.mp4` using `ffmpeg` (with a built-in fallback).

## Repository Layout

- `m3u8dl.go` — the entire CLI (cobra) and core download logic in one file.
- `go.mod` / `go.sum` — module `github.com/dingdayu/m3u8dl`, requires Go 1.27+.
- `.goreleaser.yml` — release build config (multi-OS binaries).
- `.github/workflows/` — CI/CD (docs, lint, test, release).
- `bin/` — built binaries (gitignored).

## Build & Test Commands

```bash
make build        # lint + build to ./bin/m3u8dl
make test         # run go test
make lint         # run golangci-lint
make install-hooks  # install git hooks (core.hooksPath -> .hooks)
make release      # run goreleaser release
```

After cloning, run `make install-hooks` (or `.hooks/install.sh`) once. This
sets `core.hooksPath` so the version-controlled hooks under `.hooks/` run on
every local commit:

- `pre-commit` — `gofmt` + `go vet` + EOF-newline / trailing-whitespace /
  LF enforcement (auto-fixes where possible).
- `commit-msg` — enforces Conventional Commits. Bypass with `SKIP=1 git commit ...`.

For editor-agnostic consistency also enable the optional `pre-commit` framework
(`.pre-commit-config.yaml`) and rely on `.gitattributes` / `.editorconfig`.

## Documentation languages

- `README.md` — English (primary)
- `README.zh.md` — 简体中文 (Simplified Chinese)
- `llms.txt` — machine-readable summary for LLM/Agent onboarding

## Code Style & Conventions

- The codebase is single-package (`main`) and single-file (`m3u8dl.go`). Keep it that way.
- Comments and documentation are written in **English** (see note below).
- User-facing CLI help text and log/error messages may stay in the project's
  target language as configured; run `m3u8dl --help` to see the current strings.
- Errors are returned as `failCode(code, format, ...)` / `exitError`; the `main`
  function maps them to non-zero exit codes.
- Uses atomic counters in hot concurrent paths (see `downloader`).

## Version Info

Version is injected at build time via ldflags. See `versionString()` in
`m3u8dl.go`. goreleaser injects `version`, `commit`, and `date`. For manual
builds these fall back to a dev description.

### For agents that automate downloads with this tool

- **Prefer `--json`**: `m3u8dl --url <m3u8> --json` prints a single JSON object
  to stdout where `ok:true` means success. Logs go to stderr.
- Use `--threads` to trade speed vs. server friendliness (default 24).
- For a list of URLs, use `--list file.txt`; each line is one m3u8 URL.
- Exit code is non-zero on failure — always check it.
- To drive the tool when it isn't the current directory, still run yes — but a
  reminder: resume works because the ts directory is kept on failure; re-running the
  same command fetches only the missing segments.

## Testing Notes

There is no test suite yet (roadmap). Do not commit `movie/`, `*.ts`, `*.mp4`,
or generated binaries — they are gitignored.

## Language note

Chinese comments were intentionally translated to English in this codebase. Keep
new comments in English. Runtime strings (help text, log messages, errors) are
left as-is unless the maintainers decide to change the target language.
