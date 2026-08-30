#!/usr/bin/env bash
# install.sh — installs m3u8dl git hooks into .git/hooks for all contributors.
#
# Usage:
#   ./hooks/install.sh
#     or
#   make install-hooks
#
# This sets core.hooksPath=.hooks so every hook lives in the repo (version
# controlled) instead of only in .git. Run once after cloning.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
git -C "$ROOT" config core.hooksPath .hooks
echo "✅ git hooks installed: core.hooksPath=$ROOT/.hooks"
echo "   Active hooks: pre-commit (gofmt/vet/style), commit-msg (conventional commits)"

echo ""
echo "Optional: also enable pre-commit framework hooks (advanced):"
echo "   pip install pre-commit && pre-commit install"
echo "   pre-commit run --all-files"
