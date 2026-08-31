# AGENTS.md

**This file is for developer agents (and contributing humans) working _on_ the
m3u8dl codebase.** Read it before writing or changing code here.

> 📌 Role split: if you are using the tool (installing, downloading with it),
> read [`llms.txt`](llms.txt) / [`README.md`](README.md) instead. This file only
> covers contributing to the source — code style, development workflow,
> committing, and open-source collaboration.

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
make install-hooks  # install git hooks via lefthook
make hooks-run    # run all hooks on all files (like CI)
make release      # run goreleaser release
```

After cloning, run `make install-hooks` once. This installs
[Lefthook](https://lefthook.dev) (a single Go binary) and sets up the
repository hooks:

- `pre-commit` — `gofmt` + `go vet` + file hygiene (LF, EOF newline,
  trailing-whitespace) + YAML/JSON validation + large-file checks.
- `commit-msg` — enforces Conventional Commits.

Bypass hooks temporarily with `git commit --no-verify` or `LEFTHOOK=0 git
commit ...` (do not leave bypassed).

## Documentation languages

- `README.md` — English (primary)
- `README.zh.md` — 简体中文 (Simplified Chinese)
- `llms.txt` — machine-readable quickstart for **users & their agents**
  (install/usage/flags). This file (`AGENTS.md`) covers **development** only.

## Code Style & Conventions

- The codebase is single-package (`main`) and single-file (`m3u8dl.go`). Keep it that way.
- Comments and documentation are written in **English** (see note below).
- User-facing CLI help text and log/error messages may stay in the project's
  target language as configured; run `m3u8dl --help` to see the current strings.
- Errors are returned as `failCode(code, format, ...)` / `exitError`; the `main`
  function maps them to non-zero exit codes.
- Uses atomic counters in hot concurrent paths (see `downloader`).

## Development Workflow (every change)

Follow this loop for any code change so PRs land clean and reviewable:

1. **Branch first** — never commit directly to `main`. Create a focused branch:
   `git checkout -b feat/my-change` (see branches below).
2. **Write the change** — keep it minimal and aligned with the conventions
   above: English comments, single-file/single-package, atomic concurrency.
3. **Format & verify** — run `make build` (runs gofmt + go vet + build). Before
   committing, ensure `make hooks-run` passes (all file hygiene checks,
   formatting, and linting).
4. **Commit with a Conventional Commits message** — the `commit-msg` hook
   enforces this. Prefix: `feat|fix|docs|style|refactor|perf|test|chore|ci|build`.
   Bypass hooks temporarily with `git commit --no-verify` (do not leave bypassed).
5. **Open a PR against `main`** using `.github/PULL_REQUEST_TEMPLATE.md`.
   CI (`ci.yml`) runs gofmt, `go vet`, golangci-lint, tests, and cross-OS builds.

### Atomic Commits (line-level)

Keep every commit **atomic, independent and complete**: one logical change per
commit, staged at the line/file level, so the history is easy to review, bisect
and revert.

- **One logical change per commit.** Split mixed work (e.g. a fix + a feature)
  into separate commits; do not bundle unrelated edits together.
- **Stage only what belongs** — use `git add <file>` or `git add -p` to stage the
  exact files/lines of one change. Do not sweep in incidental
  formatting/whitespace/refactor noise in the same commit.
- **Every commit leaves the tree working** — each commit should build and pass
  `make hooks-run` / `go vet`, so any intermediate commit can be checked out or
  bisected safely, and reverted without collateral damage.
- **Scoped, descriptive subject** — each message's `<scope>`/body reflects exactly
  that one change, understandable in isolation.

## Branching & Versioning

- `main` is the default; merge only via reviewed PRs.
- Use [Conventional Commits](CONTRIBUTING.md#commit-messages) so releases and
  changelogs are generated cleanly.
- Releases are automated by [release-please](.github/workflows/release-please.yml):
  every merge to `main` updates a release PR (version bump + CHANGELOG.md).
  Merging that PR creates a **draft** release + tag, which triggers
  [.github/workflows/release.yml](.github/workflows/release.yml) to build the
  artifacts once (goreleaser `--skip=publish`), attest them, publish to npm,
  and finally publish the draft with the binaries attached. Do not manually
  push `v*` tags, edit `dist/`, or commit binaries — and do not hand-edit the
  release-please sections of `CHANGELOG.md`.
- npm publishing uses [trusted publishing (OIDC)](https://docs.npmjs.com/trusted-publishers):
  the workflow grants `id-token: write` and npm signs a provenance
  attestation automatically, so there is **no `NPM_TOKEN` secret** and no
  `--provenance` flag. Each package must be linked to this repo/workflow on
  npmjs.com (Trusted Publishers setting).

## Open-Source Collaboration & Code Review

- Be kind and specific in reviews; explain _why_, suggest concrete improvements.
- Respect the [CONTRIBUTING.md](CONTRIBUTING.md) guidelines and the
  [Contributor Covenant](CODE_OF_CONDUCT.md).
- Keep PRs small and single-purpose; rebase on `main` and resolve locally.
- Never commit secrets, tokens, credentials, or third-party media.
- When reviewing, verify: no data races (`go test -race`), errors handled via
  `failCode`/`exitError`, and no stray generated files staged.

## Version Info

Version is injected at build time via ldflags. See `versionString()` in
`m3u8dl.go`. goreleaser injects `version`, `commit`, and `date`. For manual
builds these fall back to a dev description.

## Testing Notes

There is no test suite yet (roadmap). Do not commit `movie/`, `*.ts`, `*.mp4`,
or generated binaries — they are gitignored.

## Language note

Chinese comments were intentionally translated to English in this codebase. Keep
new comments in English. Runtime strings (help text, log messages, errors) are
left as-is unless the maintainers decide to change the target language.
