#!/usr/bin/env bash
# scripts/npm-pack.sh — prepare npm packages from goreleaser dist output.
#
# Usage: scripts/npm-pack.sh <version>
#   <version>  Semver version without the leading "v" (e.g. 1.2.3).
#
# Expected layout (created by goreleaser; build dirs may carry Go micro-arch
# suffixes like _v1 / _v8.0):
#   dist/
#     m3u8dl_darwin_arm64_v8.0/m3u8dl
#     m3u8dl_darwin_amd64_v1/m3u8dl
#     m3u8dl_linux_arm64_v8.0/m3u8dl
#     m3u8dl_linux_amd64_v1/m3u8dl
#     m3u8dl_windows_amd64_v1/m3u8dl.exe
#
# Packages are assembled in a staging dir (dist/npm/staging) so the committed
# package.json files in npm/ keep their 0.0.0 placeholder versions untouched.
# Each platform package also records its binary SHA-256 in package.json so the
# launcher can verify integrity at install/run time.
#
# Output (one tarball per package, all at the same <version>; npm derives the
# tarball name from the scoped package name, e.g. dingdayu-m3u8dl-*.tgz):
#   dist/npm/dingdayu-m3u8dl-<version>.tgz                  (meta launcher)
#   dist/npm/dingdayu-m3u8dl-<platform>-<version>.tgz       (platform binary packages)
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <version>" >&2
  exit 1
fi
VERSION="$1"

# Safety guard: only accept a clean semver (from a real v* git tag in CI).
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ || "$VERSION" == "0.0.0" ]]; then
  echo "error: refusing to pack placeholder/test version '${VERSION}'" >&2
  echo "       expected a clean semver like 1.2.3 (from a v* git tag)" >&2
  exit 1
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${ROOT}/dist"
NPM_OUT="${DIST}/npm"
STAGING="${NPM_OUT}/staging"
PKG="${ROOT}/npm"
PLATFORMS_DIR="${PKG}/platforms"

# sha256_file <path> — print the lowercase hex SHA-256 of a file.
sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# Map goreleaser build dirs (may carry Go micro-arch suffixes like _v1, _v8.0)
# to npm platform package names.
declare -A MAP=(
  ["m3u8dl_darwin_arm64"]="darwin-arm64"
  ["m3u8dl_darwin_amd64"]="darwin-x64"
  ["m3u8dl_linux_arm64"]="linux-arm64"
  ["m3u8dl_linux_amd64"]="linux-x64"
  ["m3u8dl_windows_amd64"]="win32-x64"
)

# find_build_dir <base-name> — locate the dist build dir, tolerating suffixes.
find_build_dir() {
  local base="$1" d
  for d in "${DIST}/${base}" "${DIST}/${base}"_*; do
    [[ -d "$d" ]] && { echo "$d"; return 0; }
  done
  echo "error: no build dir matching ${DIST}/${base}*" >&2
  return 1
}

# stage_dir <platform> — copy the committed package template into staging.
#   <platform> is "meta" for the launcher package, else a platform name.
stage_dir() {
  local platform="$1" src dest
  if [[ "$platform" == "meta" ]]; then
    src="$PKG"
    dest="${STAGING}/meta"
  else
    src="${PLATFORMS_DIR}/${platform}"
    dest="${STAGING}/${platform}"
  fi
  rm -rf "$dest"
  mkdir -p "$dest"
  cp -R "${src}/." "$dest"
  echo "$dest"
}

# pack_platform <distdir> <platform> — stage binary+manifest, inject the
# version and the binary SHA-256, then npm pack from the staging dir.
pack_platform() {
  local distdir="$1" platform="$2"
  local src
  src="$(find_build_dir "$distdir")"
  local pkgdir
  pkgdir="$(stage_dir "$platform")"
  local binname="m3u8dl"
  [[ "$platform" == win32-* ]] && binname="m3u8dl.exe"

  if [[ ! -f "${src}/${binname}" ]]; then
    echo "error: missing binary ${src}/${binname}" >&2
    exit 1
  fi

  rm -rf "${pkgdir}/bin"
  mkdir -p "${pkgdir}/bin"
  cp "${src}/${binname}" "${pkgdir}/bin/"
  chmod +x "${pkgdir}/bin/${binname}"

  local digest
  digest="$(sha256_file "${pkgdir}/bin/${binname}")"
  (
    cd "$pkgdir"
    npm pkg set "version=${VERSION}" "m3u8dl.binsha256=${digest}" >/dev/null
  )
  (cd "$pkgdir" && npm pack --pack-destination "$NPM_OUT" >/dev/null)
  echo "packed ${platform} (bin sha256 ${digest})"
}

mkdir -p "$NPM_OUT"

for distdir in "${!MAP[@]}"; do
  pack_platform "$distdir" "${MAP[$distdir]}"
done

# Meta launcher: stage it, bump version, sync optionalDependencies to the same
# version — all inside the staging copy, never touching the committed files.
META_DIR="$(stage_dir meta)"
for p in darwin-arm64 darwin-x64 linux-arm64 linux-x64 win32-x64; do
  (cd "$META_DIR" && npm pkg set "version=${VERSION}" "optionalDependencies.@dingdayu/m3u8dl-${p}=${VERSION}" >/dev/null)
done
(cd "$META_DIR" && npm pack --pack-destination "$NPM_OUT" >/dev/null)
echo "packed meta (@dingdayu/m3u8dl)"

echo "npm packages are in ${NPM_OUT}"
