# Troubleshooting

Runtime error/log strings are in Chinese; translations below identify them.
Match on the keywords given.

## "下载失败（ts 片段缺失或目录无效），ts 目录已保留可重跑续传"

_Download failed (missing/invalid TS segments); the ts directory is kept for resume._

Some segments failed after 5 retries. **Fix: re-run the exact same command** —
already-downloaded segments are kept and only missing ones are fetched. If it
keeps failing: lower `--threads 8`, raise `--timeout`, and check the URL is
still valid in a browser.

## "[Failed] 下载目录无有效 ts 文件，请检查url地址有效性"

_No valid TS files in the download dir — check the URL._

The playlist produced zero downloadable segments. Causes: expired signed URL,
wrong segment path resolution (try `--host-type v1` or `--host-type auto`), or
the URL is a master playlist whose variants need referer/cookie to be reached.
Verify the URL returns an `#EXTM3U` body: `curl -s <url> | head -3`.

## HTTP 403 / segments denied

CDN header gating. Send the player's headers: `-r <referer>` (often the site
origin, not the CDN host), `-c "<cookie>"`, sometimes `--user-agent "<ua>"` or
extra `-H "Key: Value"`.

## TLS errors / x509

The host has a broken/self-signed chain. Confirm with the user, then add
`-s/--insecure` as a last resort.

## "[Error] 解析 m3u8 地址失败"

_m3u8 URL parse failed._ The argument is not an `http(s)` URL, or the playlist
is malformed/gzip-corrupt. Quote the URL, re-check it.

## "缺少 m3u8 地址..."

_Missing m3u8 URL._ Pass `-u <url>` or `--list <file>`.

## "读取列表失败" / "列表为空"

_List file unreadable / empty_ (batch mode). Check the file path and that it
contains at least one URL.

## "[FFmpeg Error]" during merge

ffmpeg is installed but rejected the concat. Causes: mixed-codec variants in
one playlist, corrupt segment. Re-run to re-download suspect segments (the ts
dir is kept), or install a current ffmpeg. Without ffmpeg at all, m3u8dl falls
back to built-in merge — usually fine, but system ffmpeg output is preferred.

## "[warn] GET 失败 ..." repeated on KEY URI

The AES key URL is unreachable (expired token / referer gating). Same fixes as
403 above — the key request uses the same headers.

## npm install: "integrity check failed" (exit 65)

The cached/installed platform binary does not match its recorded SHA-256.
Remove and reinstall from the registry; do not proceed with that binary.

## Slow or stuck downloads

- Raise `--threads` (32–64) on fast CDNs; each segment retries 5× internally.
- `--timeout 60` shortens per-request stalls on dead connections.
- Master playlist with several variants: if the resolved variant is not the
  one the user wants (e.g. the largest), point `-u` directly at the chosen
  variant's `.m3u8` URL instead.

## Debugging residue

Run once with `--clean-ts=false` to keep the TS segments after (or instead
of) merging, then inspect them (`ffprobe 00001.ts`) to see whether the problem
is download-side or merge-side.
