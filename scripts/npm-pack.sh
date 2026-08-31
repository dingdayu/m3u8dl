#!/usr/bin/env bash
# scripts/npm-pack.sh — prepare npm packages from goreleaser dist output.
#
# Usage: scripts/npm-pack.sh <version>
#   <version>  Semver version without the leading "v" (e.g. 1.2.3).
#
# Expected layout (created by goreleaser):
#   dist/
#     m3u8dl_darwin_arm64_v8.0/m3u8dl
#     m3u8dl_darwin_amd64_v1/m3u8dl
#     m3u8dl_linux_arm64_v8.0/m3u8dl
#     m3u8dl_linux_amd64_v1/m3u8dl
#     m3u8dl_windows_amd64_v1/m3u8dl.exe
#
# Output (one tarball per package, all at the same <version>):
#   dist/npm/m3u8dl-<version>.tgz                  (meta launcher)
#   dist/npm/m3u8dl-<platform>-<version>.tgz       (platform binary packages)
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
PKG="${ROOT}/npm"
PLATFORMS_DIR="${PKG}/platforms"

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

# update_version <dir> — set version in package.json.
update_version() {
  local dir="$1"
  (cd "$dir" && npm pkg set "version=${VERSION}" >/dev/null)
}

# pack_platform <distdir> <platform> — stage the binary, bump version, npm pack.
pack_platform() {
  local distdir="$1" platform="$2"
  local src
  src="$(find_build_dir "$distdir")"
  local pkgdir="${PLATFORMS_DIR}/${platform}"
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

  update_version "$pkgdir"
  (cd "$pkgdir" && npm pack --pack-destination "$NPM_OUT" >/dev/null)
  echo "packed ${platform}"
}

mkdir -p "$NPM_OUT"

for distdir in "${!MAP[@]}"; do
  pack_platform "$distdir" "${MAP[$distdir]}"
done

# Meta launcher: bump version, sync optionalDependencies to the same version.
update_version "$PKG"
for p in darwin-arm64 darwin-x64 linux-arm64 linux-x64 win32-x64; do
  (cd "$PKG" && npm pkg set "optionalDependencies.m3u8dl-${p}=${VERSION}" >/dev/null)
done
(cd "$PKG" && npm pack --pack-destination "$NPM_OUT" >/dev/null)
echo "packed meta (m3u8dl)"

echo "npm packages are in ${NPM_OUT}"
