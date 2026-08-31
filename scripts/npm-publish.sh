#!/usr/bin/env bash
# scripts/npm-publish.sh — publish all packed npm packages to the registry.
#
# Usage: scripts/npm-publish.sh <version>
#   <version>  Semver version without the leading "v" (e.g. 1.2.3).
#
# Publishes every tarball produced by scripts/npm-pack.sh with --provenance
# (requires npm >= 9.5 and an id-token-capable CI environment; the release
# workflow already grants id-token: write).
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <version>" >&2
  exit 1
fi
VERSION="$1"

# Safety guard: never publish a placeholder/test version. Real releases come
# only from a semver git tag (e.g. 1.2.3) injected by the CI workflow.
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ || "$VERSION" == "0.0.0" ]]; then
  echo "error: refusing to publish invalid version '${VERSION}'" >&2
  echo "       expected a clean semver like 1.2.3 (from a v* git tag)" >&2
  exit 1
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NPM_OUT="${ROOT}/dist/npm"

if [[ ! -d "$NPM_OUT" ]]; then
  echo "error: ${NPM_OUT} not found; run scripts/npm-pack.sh first" >&2
  exit 1
fi

shopt -s nullglob
tarballs=("${NPM_OUT}"/*.tgz)
if [[ ${#tarballs[@]} -ne 6 ]]; then
  echo "error: expected 6 .tgz files in ${NPM_OUT}, got ${#tarballs[@]}" >&2
  exit 1
fi

for tgz in "${tarballs[@]}"; do
  echo "publishing $(basename "$tgz")"
  npm publish "$tgz" --access public --provenance
done

echo "published m3u8dl ${VERSION} to npm"
