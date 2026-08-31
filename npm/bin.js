#!/usr/bin/env node
// m3u8dl npm launcher: resolves the platform-specific binary package (an
// optionalDependency) and executes it, forwarding all arguments and the exit
// code. This keeps each install to a single ~3.7MB binary download.
//
// The platform package exposes an index.js that exports the absolute binary
// path, so resolution works under npm, pnpm and Yarn PnP alike. Before
// executing, the binary's SHA-256 is checked against the digest recorded in
// the platform package's package.json ("m3u8dl".binsha256) at pack time, so a
// corrupted or tampered download fails loudly instead of running.
'use strict';

const { spawnSync } = require('child_process');
const fs = require('fs');
const crypto = require('crypto');

// npm platform/arch identifiers used in optionalDependencies package names.
const PLATFORMS = {
  'darwin:arm64': '@dingdayu/m3u8dl-darwin-arm64',
  'darwin:x64': '@dingdayu/m3u8dl-darwin-x64',
  'linux:arm64': '@dingdayu/m3u8dl-linux-arm64',
  'linux:x64': '@dingdayu/m3u8dl-linux-x64',
  'win32:x64': '@dingdayu/m3u8dl-win32-x64',
};

const key = `${process.platform}:${process.arch}`;
const pkg = PLATFORMS[key];

function fail(msg, code = 1) {
  console.error(`m3u8dl: ${msg}`);
  process.exit(code);
}

if (!pkg) {
  fail(
    `unsupported platform "${key}".\n` +
      'Supported: darwin-arm64, darwin-x64, linux-arm64, linux-x64, win32-x64.\n' +
      'Install from source instead: go install github.com/dingdayu/m3u8dl@latest'
  );
}

let bin;
try {
  // The platform package's index.js resolves __dirname-relative paths for us.
  bin = require(pkg);
} catch {
  fail(
    `platform package ${pkg} is not installed.\n` +
      'If you used --no-optional, reinstall without it: npm install @dingdayu/m3u8dl'
  );
}

// Recorded at pack time by scripts/npm-pack.sh ("m3u8dl".binsha256).
let expected;
try {
  const manifest = require(`${pkg}/package.json`);
  expected = manifest.m3u8dl && manifest.m3u8dl.binsha256;
} catch {
  expected = undefined;
}

if (!fs.existsSync(bin)) {
  fail(`binary not found: ${bin}`);
}

if (expected) {
  const actual = crypto.createHash('sha256').update(fs.readFileSync(bin)).digest('hex');
  if (actual !== expected) {
    fail(
      `integrity check failed for ${bin}\n` +
        `  expected sha256: ${expected}\n` +
        `  actual   sha256: ${actual}\n` +
        'The downloaded binary is corrupted or was tampered with.\n' +
        'Reinstall from a trusted registry: npm install -g @dingdayu/m3u8dl',
      65
    );
  }
}

const res = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
if (res.error) {
  fail(`failed to launch: ${res.error.message}`);
}
process.exit(res.status ?? 1);
