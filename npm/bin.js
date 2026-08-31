#!/usr/bin/env node
// m3u8dl npm launcher: resolves the platform-specific binary package (an
// optionalDependency) and executes it, forwarding all arguments and the exit
// code. This keeps each install to a single ~3.7MB binary download.
'use strict';

const { spawnSync } = require('child_process');
const path = require('path');
const fs = require('fs');

// npm platform/arch identifiers used in optionalDependencies package names.
const PLATFORMS = {
  'darwin:arm64': 'm3u8dl-darwin-arm64',
  'darwin:x64': 'm3u8dl-darwin-x64',
  'linux:arm64': 'm3u8dl-linux-arm64',
  'linux:x64': 'm3u8dl-linux-x64',
  'win32:x64': 'm3u8dl-win32-x64',
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
  const pkgJson = require.resolve(`${pkg}/package.json`);
  const dir = path.dirname(pkgJson);
  const name = process.platform === 'win32' ? 'm3u8dl.exe' : 'm3u8dl';
  bin = path.join(dir, 'bin', name);
} catch {
  fail(
    `platform package ${pkg} is not installed.\n` +
      'If you used --no-optional, reinstall without it: npm install m3u8dl'
  );
}

if (!fs.existsSync(bin)) {
  fail(`binary not found: ${bin}`);
}

const res = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
if (res.error) {
  fail(`failed to launch: ${res.error.message}`);
}
process.exit(res.status ?? 1);
