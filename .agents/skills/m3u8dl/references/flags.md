# m3u8dl flags reference

```
m3u8dl [flags] <m3u8-url> [outputName]
```

| Flag           | Short | Default   | Meaning                                                                                                                                                                                                                         |
| -------------- | ----- | --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--url`        | `-u`  | —         | m3u8 URL (`http(s)://.../index.m3u8`). Also accepted as first positional arg.                                                                                                                                                   |
| `--threads`    | `-n`  | `24`      | Concurrent TS-segment downloads. Lower (8) for flaky/strict hosts, raise (32–64) for fast CDNs.                                                                                                                                 |
| `--output`     | `-o`  | `movie`   | Output MP4 base name, no extension. Also accepted as second positional arg.                                                                                                                                                     |
| `--save-path`  | —     | cwd       | Output directory. Must already exist. Legacy: `-sp`.                                                                                                                                                                            |
| `--host-type`  | —     | `v2`      | How relative TS/KEY URIs are resolved: `v2` = against `scheme://host`; `v1` = against `scheme://host/<dir-of-m3u8>`; `auto` = `v1` first, falling back to `v2` on failure. Try the other mode when segments 404. Legacy: `-ht`. |
| `--cookie`     | `-c`  | —         | `Cookie` request header for hosts that gate segments.                                                                                                                                                                           |
| `--referer`    | `-r`  | m3u8 host | `Referer` header. Many CDNs reject segments without the player's referer.                                                                                                                                                       |
| `--user-agent` | —     | Chrome UA | `User-Agent` header. Legacy: `-ua`.                                                                                                                                                                                             |
| `--header`     | `-H`  | —         | Extra header, repeatable: `-H "Key: Value"`.                                                                                                                                                                                    |
| `--timeout`    | `-t`  | `120`     | Per-request timeout in seconds.                                                                                                                                                                                                 |
| `--insecure`   | `-s`  | off       | Skip TLS verification (self-signed hosts only).                                                                                                                                                                                 |
| `--purge-dup`  | —     | off       | Scan and remove ALL occurrences of duplicate-content (ad) segments by hash. Legacy: `-pd`.                                                                                                                                      |
| `--clean-ts`   | —     | true      | Delete the TS dir after successful merge (keep for debugging with `--clean-ts=false`).                                                                                                                                          |
| `--json`       | `-j`  | off       | Print one structured JSON result to stdout; logs stay on stderr.                                                                                                                                                                |
| `--list`       | `-l`  | —         | Batch file, one m3u8 URL per line. Per-URL output names are `<output>_NNN.mp4`.                                                                                                                                                 |
| `--version`    | —     | —         | Print version (also `m3u8dl version <x>` template).                                                                                                                                                                             |
| `--help`       | `-h`  | —         | Full help.                                                                                                                                                                                                                      |

Legacy compatibility: old multi-char single-dash flags (`-ht`, `-sp`, `-pd`,
`-ua`, ...) are rewritten to the semantic long flags automatically, so
existing scripts keep working.

## Master playlists & encryption

- Master playlists (nested m3u8) are followed automatically; the variant is
  resolved from the playlist.
- AES-128 segment encryption (`#EXT-X-KEY`) is decrypted automatically as
  long as the KEY URI is reachable with the same headers.
- `#EXT-X-MAP` init segments and media initialization are handled during merge.

## Batch list file

Plain text, whitespace-separated URLs (one per line). Behavior:

- A URL whose `<save-path>/<output>_NNN.mp4` already exists is **skipped**.
- Any single failure makes the whole run exit non-zero with `ok:false`; the
  `error` string and stderr log carry `成功=N 跳过=N 失败=N` plus the failed
  URL list. Re-running resumes/skips and retries only the failures.
