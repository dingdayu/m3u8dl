#!/usr/bin/env bash
# scripts/bin-checksums.sh — record SHA-256 digests of the raw m3u8dl binaries
# inside goreleaser's dist build dirs.
#
# Usage: scripts/bin-checksums.sh
#
# goreleaser's checksums.txt only covers the release archives (.tar.gz/.zip),
# not the binaries packed into the per-platform npm packages. This manifest
# covers those raw binaries, so binaries fetched standalone (e.g. from the
# jsDelivr CDN) can be verified against a release-published, attested file:
#
#   curl -fSLO https://github.com/dingdayu/m3u8dl/releases/download/v1.2.3/bin-checksums.txt
#   curl -fSL -o m3u8dl https://cdn.jsdelivr.net/npm/@dingdayu/m3u8dl-linux-x64@1.2.3/bin/m3u8dl
#   sha256sum -c bin-checksums.txt --ignore-missing
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${ROOT}/dist"
OUT="${DIST}/bin-checksums.txt"

# sha256_file <path> — print the lowercase hex SHA-256 of a file.
sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# Map goreleaser build dirs (may carry Go micro-arch suffixes like _v1, _v8.0)
# to the npm platform package names used on jsDelivr.
declare -A MAP=(
  ["m3u8dl_darwin_arm64"]="darwin-arm64"
  ["m3u8dl_darwin_amd64"]="darwin-x64"
  ["m3u8dl_linux_arm64"]="linux-arm64"
  ["m3u8dl_linux_amd64"]="linux-x64"
  ["m3u8dl_windows_amd64"]="win32-x64"
)

find_build_dir() {
  local base="$1" d
  for d in "${DIST}/${base}" "${DIST}/${base}"_*; do
    [[ -d "$d" ]] && { echo "$d"; return 0; }
  done
  echo "error: no build dir matching ${DIST}/${base}*" >&2
  return 1
}

: >"$OUT"
for base in "${!MAP[@]}"; do
  platform="${MAP[$base]}"
  binname="m3u8dl"
  [[ "$platform" == win32-* ]] && binname="m3u8dl.exe"
  src="$(find_build_dir "$base")"
  if [[ ! -f "${src}/${binname}" ]]; then
    echo "error: missing binary ${src}/${binname}" >&2
    exit 1
  fi
  # Name entries after the jsDelivr path so users can eyeball the mapping.
  printf '%s  @dingdayu/m3u8dl-%s/bin/%s\n' "$(sha256_file "${src}/${binname}")" "$platform" "$binname" >>"$OUT"
done

sort -k2 -o "$OUT" "$OUT"
echo "wrote ${OUT}"
cat "$OUT"
