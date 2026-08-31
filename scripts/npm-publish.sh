#!/usr/bin/env bash
# scripts/npm-publish.sh — publish all packed npm packages to the registry.
#
# Usage: scripts/npm-publish.sh <version>
#   <version>  Semver version without the leading "v" (e.g. 1.2.3).
#
# Publishes every tarball produced by scripts/npm-pack.sh via npm **trusted
# publishing** (OIDC). No long-lived npm token is used: the release workflow
# grants `id-token: write`, and npm (>= 11.5.1, bundled with the workflow's
# Node) automatically signs a provenance attestation from the OIDC identity —
# so there is intentionally no `--provenance` flag and no NODE_AUTH_TOKEN.
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
  # Resolve the real package name from inside the tarball, and skip it if this
  # version is already on the registry (makes re-runs after a partial
  # failure idempotent).
  pkg_name="$(tar -xzOf "$tgz" package/package.json | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>console.log(JSON.parse(d).name))')"
  if npm view "${pkg_name}@${VERSION}" version >/dev/null 2>&1; then
    echo "skipping ${pkg_name}@${VERSION} (already published)"
    continue
  fi
  echo "publishing $(basename "$tgz")"
  # Trusted publishing: no token, provenance is automatic.
  npm publish "$tgz" --access public
done

echo "published @dingdayu/m3u8dl ${VERSION} to npm"
