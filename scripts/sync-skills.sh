#!/usr/bin/env bash
# scripts/sync-skills.sh — keep agent-skill vendor mirrors in sync with the
# canonical skill sources.
#
# Canonical source of truth:  .agents/skills/<name>/   (Agent Skills open standard,
#                                                      https://agentskills.io —
#                                                      scanned directly by VS Code /
#                                                      GitHub Copilot, Copilot CLI,
#                                                      Codex, Gemini CLI, OpenCode…)
# Generated mirror:           .claude/skills/<name>/   (Claude Code, which only
#                                                      discovers .claude/skills/)
#
# Rather than duplicate content by hand, edit ONLY .agents/skills/<name>/ and
# run this script to regenerate the mirror. CI runs it in --check mode so the
# mirror can never drift from the source.
#
# Usage:
#   scripts/sync-skills.sh           # copy canonical -> mirrors
#   scripts/sync-skills.sh --check   # fail if mirrors differ from canonical
set -euo pipefail

MODE="sync"
if [[ "${1:-}" == "--check" ]]; then
  MODE="check"
elif [[ $# -gt 0 ]]; then
  echo "usage: $0 [--check]" >&2
  exit 2
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC_DIR="${ROOT}/.agents/skills"
MIRROR_DIRS=("${ROOT}/.claude/skills")

drift=0

check_skill() {
  local mirror="$1" name="$2"
  if ! diff -r -q "${SRC_DIR}/${name}" "${mirror}/${name}" >/dev/null 2>&1; then
    echo "DRIFT: ${mirror#"${ROOT}/"}/${name} differs from .agents/skills/${name}" >&2
    drift=1
  fi
}

sync_skill() {
  local mirror="$1" name="$2"
  mkdir -p "$mirror"
  rm -rf "${mirror:?}/${name}"
  cp -R "${SRC_DIR}/${name}" "${mirror}/${name}"
}

# 1. Every canonical skill must exist (and match) in each mirror.
shopt -s nullglob
canon=("${SRC_DIR}"/*/)
if [[ ${#canon[@]} -eq 0 ]]; then
  echo "error: no skills found under ${SRC_DIR#"${ROOT}/"}" >&2
  exit 1
fi

for skill_dir in "${canon[@]}"; do
  name="$(basename "$skill_dir")"
  [[ -f "${skill_dir}SKILL.md" ]] || { echo "error: ${name}/ has no SKILL.md" >&2; exit 1; }
  for mirror in "${MIRROR_DIRS[@]}"; do
    if [[ "$MODE" == "check" ]]; then
      if [[ ! -d "${mirror}/${name}" ]]; then
        echo "DRIFT: missing mirror ${mirror#"${ROOT}/"}/${name} (run scripts/sync-skills.sh)" >&2
        drift=1
      else
        check_skill "$mirror" "$name"
      fi
    else
      sync_skill "$mirror" "$name"
    fi
  done
done

# 2. Mirrors must not contain extra skills the canonical dir does not have.
if [[ "$MODE" == "check" ]]; then
  for mirror in "${MIRROR_DIRS[@]}"; do
    [[ -d "$mirror" ]] || continue
    for mdir in "${mirror}"/*/; do
      [[ -d "$mdir" ]] || continue
      name="$(basename "$mdir")"
      if [[ ! -d "${SRC_DIR}/${name}" ]]; then
        echo "DRIFT: ${mirror#"${ROOT}/"}/${name} has no canonical source under .agents/skills" >&2
        drift=1
      fi
    done
  done
fi

if [[ "$MODE" == "check" ]]; then
  if [[ "$drift" -ne 0 ]]; then
    echo "skills mirrors are out of sync — edit .agents/skills/<name>/ then run scripts/sync-skills.sh" >&2
    exit 1
  fi
  echo "skills mirrors are in sync."
else
  echo "synced .agents/skills -> ${MIRROR_DIRS[*]#"${ROOT}/"}"
fi
