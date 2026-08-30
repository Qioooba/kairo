'use strict';

const fs = require('fs');

const output = process.argv[2];
if (!output) {
  process.stderr.write('missing output path\n');
  process.exit(2);
}
fs.writeFileSync(output, 'kairo-e2e-reminder', 'utf8');
