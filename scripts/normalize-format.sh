#!/usr/bin/env bash
# normalize-format.sh — normalize textual file format for m3u8dl.
#
# Ensures a consistent, editor-agnostic baseline across the repo:
#   - LF line endings (no CRLF)
#   - exactly one trailing newline at EOF
#   - no trailing whitespace (space/tab at line ends)  [check-only via --check]
#
# Usage:
#   scripts/normalize-format.sh          # apply fixes in place
#   scripts/normalize-format.sh --check  # only report; exit 1 if anything dirty
#
# This mirrors what the git pre-commit hook enforces (see .hooks/pre-commit).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

CHECK_ONLY=0
[ "${1:-}" = "--check" ] && CHECK_ONLY=1

# Files to process: all tracked text-like files, minus gitignored/generated/bin.
mapfile -t FILES < <(
  git ls-files --cached --others --exclude-standard \
    -- ':!bin' ':!dist' \
    '(e) *.mp4' '(e) *.ts' '(e) *.part' \
    '(e) *.png' '(e) *.jpg' '(e) *.jpeg' '(e) *.gif' \
    '(e) *.zip' '(e) *.tar.gz' '(e) *.exe' \
    || true
)

# Fallback: if git isn't available or no files, walk the tree.
if [ "${#FILES[@]}" -eq 0 ]; then
  mapfile -t FILES < <(
    find . -type f \
      -not -path './.git/*' -not -path './bin/*' -not -path './dist/*' \
      -not -name '*.mp4' -not -name '*.ts' -not -name '*.part' \
      -not -name '*.png' -not -name '*.jpg' -not -name '*.jpeg' \
      -not -name '*.gif' -not -name '*.zip' -not -name '*.tar.gz' \
      -not -name '*.exe' -not -name '.DS_Store'
  )
fi

changed=0
for f in "${FILES[@]}"; do
  [ -f "$f" ] || continue
  # Skip binary files (contains NUL).
  if LC_ALL=C grep -q $'\x00' "$f" 2>/dev/null; then
    continue
  fi

  # 1) CRLF -> LF
  if grep -q $'\r\n' "$f" 2>/dev/null; then
    if [ "$CHECK_ONLY" = "1" ]; then
      echo "CRLF: $f"
    else
      perl -pi -e 's/\r\n/\n/g' "$f"
      echo "fixed CRLF: $f"
    fi
    changed=1
  fi

  # 2) trailing whitespace
  if grep -nE ' +$|\t+$' "$f" >/dev/null 2>&1; then
    if [ "$CHECK_ONLY" = "1" ]; then
      echo "trailing-whitespace: $f"
    else
      perl -pi -e 's/[ \t]+$//' "$f"
      echo "fixed trailing-whitespace: $f"
    fi
    changed=1
  fi

  # 3) EOF newline: ensure exactly one trailing newline
  if [ -n "$(tail -c 1 "$f" 2>/dev/null)" ]; then
    if [ "$CHECK_ONLY" = "1" ]; then
      echo "EOF-newline: $f"
    else
      printf '\n' >> "$f"
      echo "fixed EOF-newline: $f"
    fi
    changed=1
  fi
done

if [ "$CHECK_ONLY" = "1" ]; then
  if [ "$changed" = "1" ]; then
    echo "Some files are not normalized. Run: scripts/normalize-format.sh"
    exit 1
  fi
  echo "All files are normalized (LF, one EOF newline, no trailing whitespace)."
  exit 0
fi

if [ "$changed" = "1" ]; then
  echo "Normalization applied. Review with: git diff --stat"
  exit 0
fi
echo "Nothing to normalize."
exit 0
