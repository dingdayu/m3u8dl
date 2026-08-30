# Contributing to m3u8dl

First off, thank you for taking the time to contribute! 🎉

The following guide describes how to contribute to m3u8dl. Please read it and
follow the spirit and the letter of the guidelines. M3u8dl is a small, focused
tool, so keep your changes aligned with its scope.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [How to Contribute](#how-to-contribute)
  - [Reporting Bugs](#reporting-bugs)
  - [Suggesting Features](#suggesting-features)
  - [Your First Code Contribution](#your-first-code-contribution)
  - [Pull Requests](#pull-requests)
- [Development Workflow](#development-workflow)
  - [Build & Test](#build--test)
  - [Commit Messages](#commit-messages)
  - [Code Style](#code-style)
  - [Adding a Release](#adding-a-release)
- [Project Layout](#project-layout)

## Code of Conduct

By participating in this project, you agree to abide by the
[Contributor Covenant](CODE_OF_CONDUCT.md). Please report unacceptable behavior
via GitHub issues.

## Getting Started

1. Fork the repository.
2. Clone your fork:
   ```bash
   git clone https://github.com/<your-username>/m3u8dl.git
   cd m3u8dl
   ```
3. Add the upstream remote:
   ```bash
   git remote add upstream https://github.com/dingdayu/m3u8dl.git
   git fetch upstream
   ```
4. Create a branch for your work:
   ```bash
   git checkout -b feature/my-feature
   ```

## How to Contribute

### Reporting Bugs

Before reporting, check the [issues](../../issues) to avoid duplicates. When you
open a new issue, please use the **Bug report** template and include:

- m3u8dl version (`m3u8dl --version`)
- OS / architecture
- The m3u8 URL (or sanitized sample) and the exact command used
- Full output (use `--json` where possible) and expected vs. actual behavior
- Any relevant log lines

### Suggesting Features

Open a **Feature request** issue. Describe the problem you want to solve, how you
envision the feature working, and ideally how it fits the existing CLI/api
shapes. Small, well-scoped features are most likely to be accepted.

### Your First Code Contribution

1. Grab an issue labeled `good first issue` or `help wanted`.
2. Comment on it that you're working on it to avoid duplicate effort.
3. Follow the [Development Workflow](#development-workflow).

### Pull Requests

1. Make sure your branch is up to date with `main`:
   ```bash
   git pull upstream main
   ```
2. Commit your changes with a clear [message](#commit-messages).
3. Push and open a **Pull Request** against `main` using the PR template.
4. Ensure CI passes (golangci-lint, `go vet`, tests, `gofmt`).
5. Respond to review feedback; keep the scope tight.

## Development Workflow

### Build & Test

```bash
make build        # gofmt check + vet + build to ./bin/m3u8dl
make test         # go test ./...
make lint         # golangci-lint run
```

Requirements: Go 1.27+ (see `go.mod`).

### Commit Messages

Follow the [Conventional Commits](https://www.conventionalcommits.org/) spec:

```
<type>(<scope>): <subject>

<body>
```

- Types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `chore`, `ci`.
- Keep the subject under 72 chars and lowercase; do not end with punctuation.
- Examples:
  - `feat: support per-segment AES keys`
  - `fix(download): resume missing segments on re-run`
  - `docs: add quick-start examples`

### Code Style

- Run `gofmt` (Go uses tabs for indentation).
- Comments are in English.
- Keep the codebase **single-file, single-package**.
- Use atomic counters in concurrent paths; avoid data races (run `go test -race`).
- New user-facing strings: keep them consistent with the project's current
  target language as configured (run `m3u8dl --help` in a working build).

### Adding a Release

Releases are automated with goreleaser via the `release` workflow. To publish:

1. Ensure `main` contains the changes you want to release and CI is green.
2. Create and push a version tag (use [semver](https://semver.org/)):
   ```bash
   git tag v1.2.3 && git push origin v1.2.3
   ```
3. The `release` GitHub Action builds binaries (linux/darwin/windows,
   amd64/arm64) and creates a GitHub Release with the changelog. See
   `.goreleaser.yml` and `.github/workflows/release.yml` for details.

   You can preview the build locally without publishing:

   ```bash
   make release-snapshot   # ghcr: builds cross-platform archives under ./dist
   ```

   > Note: `make app-version` prints the version derived from the latest tag
   > (e.g. `1.2.3`) — useful for shell scripts that need it.

## Project Layout

- `m3u8dl.go` — entire CLI (cobra) and core download logic.
- `go.mod` / `go.sum` — module and dependencies.
- `.goreleaser.yml` — release build configuration.
- `.github/workflows/` — CI/CD pipelines.
- `bin/` — build output (gitignored).

_Questions? Open an issue tagged `question`._