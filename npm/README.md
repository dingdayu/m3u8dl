# m3u8dl

A fast, multi-threaded **m3u8 video downloader** written in Go.

This is the installable `m3u8dl` package. It pulls the platform-specific
binary package for your OS/arch (`darwin-arm64`, `darwin-x64`, `linux-arm64`,
`linux-x64` or `win32-x64`) as an optional dependency — npm downloads only the
binary you need, and exposes it as the `m3u8dl` CLI.

## Install

```bash
npm install -g m3u8dl
```

Or run once without installing:

```bash
npx m3u8dl --help
```

> In China, use the npmmirror registry for speed:
> `npm install -g m3u8dl --registry=https://registry.npmmirror.com`

## Usage

```bash
m3u8dl --url https://example.com/index.m3u8 --threads 16 --json
```

See the full documentation on GitHub:
https://github.com/dingdayu/m3u8dl

All packages are built by the project's GitHub Actions workflow and published
with npm **provenance**; verify with `npm audit signatures`.

## License

MIT © dingdayu