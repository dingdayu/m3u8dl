'use strict';
// Resolves the bundled m3u8dl binary for this platform. The launcher requires
// this package and uses the returned absolute path, which keeps binary
// resolution working under npm, pnpm and Yarn PnP.
const path = require('path');
module.exports = path.join(__dirname, 'bin', 'm3u8dl');
