# JSON output schema (`--json`)

With `--json`, **stdout contains exactly one JSON object** (the final result);
progress, warnings, and error logs all go to **stderr**. Parse stdout only.

## Success (single)

```json
{
  "ok": true,
  "path": "/abs/dir/movie.mp4",
  "duration_sec": 12.34,
  "mode": "single",
  "name": "movie",
  "url": "https://example.com/index.m3u8"
}
```

## Success (batch)

```json
{
  "ok": true,
  "error": "批量完成: 成功=3 跳过=1 失败=0",
  "duration_sec": 98.7,
  "mode": "batch"
}
```

Note: in batch mode the summary string (Chinese: "batch done:
success/skipped/failed counts") is reported in the `error` field **even on
success** — trust `ok`, not the presence of `error`.

## Failure

```json
{ "ok": false, "error": "<message>" }
```

Failures exit non-zero (usually `1`; npm launcher integrity failures exit
`65`). Cobra usage errors also produce a `{"ok":false,...}` object on stdout.

## Fields

| Field          | Type   | Present when               | Meaning                                                       |
| -------------- | ------ | -------------------------- | ------------------------------------------------------------- |
| `ok`           | bool   | always                     | `true` only when the merge produced the MP4.                  |
| `path`         | string | single success             | Absolute path of the merged `.mp4`.                           |
| `duration_sec` | number | success                    | Wall-clock seconds for the run.                               |
| `mode`         | string | success                    | `"single"` or `"batch"`.                                      |
| `name`         | string | single success             | Output base name used.                                        |
| `url`          | string | single success             | The m3u8 URL that was fetched.                                |
| `error`        | string | failure (or batch summary) | Human-readable message (project runtime strings are Chinese). |

## Decision rule for agents

```text
exit_code == 0 AND stdout JSON has "ok":true   →  success; report path + duration
otherwise                                      →  failure; inspect error + stderr
```
