# Security Policy

## Supported Versions

We provide security updates for the latest release and, where practical, the
most recent minor release.

| Version  | Supported |
| -------- | --------- |
| latest   | ✅        |
| < latest | ❌        |

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**
Instead, please report them privately to minimize risk to users:

- GitHub Security Advisories:
  https://github.com/dingdayu/m3u8dl/security/advisories/new

We ask you to include:

- The affected version(s).
- A clear description of the vulnerability and the attack scenario.
- Steps to reproduce (sanitize any sensitive URLs/data).
- Suggested fix (if you have one).

### What to expect

- An acknowledgment/reply within **3 business days**.
- A timeline commitment for a fix and disclosure.
- Credit in any changelog/release notes (unless you prefer anonymity).

## Scope

Relevant areas: m3u8/TS URL handling, HTTP transport (headers, TLS), file
handling (paths, merges), and any input parsed from remote playlists. Do not
include credentials or private stream URLs in your report or reproduction
sample — redact them.

## Disclosure

We follow responsible disclosure: we coordinate the fix and agree on a
disclosure date before publishing details publicly.
