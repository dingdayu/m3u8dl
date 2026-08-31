# Installing m3u8dl

Pick the first channel that fits the user's environment. All channels publish
the same signed release artifacts; verify when in doubt.

## Channel 1 — npm (cross-platform, fastest for most users)

```bash
npm install -g @dingdayu/m3u8dl
m3u8dl --version
```

Supported platforms: darwin-arm64, darwin-x64, linux-arm64, linux-x64,
win32-x64. The launcher re-checks the platform binary's SHA-256 on every run
and aborts on mismatch. One-shot usage without installing:
`npx @dingdayu/m3u8dl --url <m3u8-url> --json`.

## Channel 2 — Go install (requires Go 1.27+)

```bash
go install github.com/dingdayu/m3u8dl@latest
```

The binary lands in `$(go env GOPATH)/bin` — make sure it is on `PATH`.

## Channel 3 — GitHub release binaries

Releases page: https://github.com/dingdayu/m3u8dl/releases
Archive naming: `m3u8dl_<ver>_<OS>_<ARCH>.tar.gz` (`.zip` on Windows), where
`<OS>` is `Linux` / `Darwin` / `Windows` and amd64 is written `x86_64`
(e.g. `m3u8dl_1.2.3_Linux_x86_64.tar.gz`).

## Channel 4 — China / slow networks

- npm via npmmirror:
  `npm install -g @dingdayu/m3u8dl --registry=https://registry.npmmirror.com`
- jsDelivr CDN binary (Cloudflare/Fastly-backed):
  `https://cdn.jsdelivr.net/npm/@dingdayu/m3u8dl-<platform>@<version>/bin/m3u8dl`
  (e.g. platform `linux-x64`)
- `go install` with a proxy: `GOPROXY=https://goproxy.cn,direct go install github.com/dingdayu/m3u8dl@latest`

## Verifying a release

Before trusting a downloaded archive or binary:

1. SHA-256: compare against `checksums.txt` (archives) or
   `bin-checksums.txt` (npm channel binaries) attached to the release.
2. GitHub Artifact Attestations:
   `gh attestation verify <file> --repo dingdayu/m3u8dl`
3. npm provenance: `npm audit signatures` after install.

Also requires (for MP4 merge quality): `ffmpeg` on `PATH`. m3u8dl works
without it via a built-in fallback merge, but system ffmpeg is recommended.

## Registering this skill with the user's agent

Every released `m3u8dl` binary embeds this skill. If the user wants their
coding agent (VS Code / Copilot, Claude Code, Codex, Gemini CLI, …) to keep
knowing how to drive the tool, install the skill once:

```bash
m3u8dl skills list                                        # what is bundled
m3u8dl skills install                                     # -> ./.agents/skills/
m3u8dl skills install --agent claude                      # -> ./.claude/skills/
m3u8dl skills install --scope user                        # -> ~/.agents/skills/
m3u8dl skills install --dir /path/to/project/.agents/skills --force
```

Use `--json` to get a machine-readable result
(`{"ok":true,"target":"...","skills":["m3u8dl"],"files":5}`). Re-running the
install fails if the skill is already there (so user edits are never
silently clobbered); add `--force` to overwrite it with the bundled version.
