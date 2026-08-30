'use strict';

// WebService 页纯函数回归：不依赖 DOM/浏览器，覆盖用户最常粘贴和切换的格式。
const fs = require('fs');
const path = require('path');
const assert = require('assert');

const source = fs.readFileSync(path.join(__dirname, 'webservice.js'), 'utf8');

function extract(name) {
  const re = new RegExp('function\\s+' + name + '\\s*\\([^)]*\\)\\s*\\{');
  const match = source.match(re);
  if (!match) throw new Error('function not found: ' + name);
  let i = source.indexOf('{', match.index) + 1;
  let depth = 1;
  let quote = '';
  let escaped = false;
  for (; i < source.length && depth > 0; i++) {
    const ch = source[i];
    if (quote) {
      if (escaped) escaped = false;
      else if (ch === '\\') escaped = true;
      else if (ch === quote) quote = '';
      continue;
    }
    if (ch === '"' || ch === "'" || ch === '`') { quote = ch; continue; }
    if (ch === '{') depth++;
    else if (ch === '}') depth--;
  }
  if (depth !== 0) throw new Error('unbalanced function: ' + name);
  return source.slice(match.index, i);
}

function load(name) {
  return new Function(extract(name) + '; return ' + name + ';')();
}

const parseHeadersText = load('parseHeadersText');
const syncXMLDeclaration = load('syncXMLDeclaration');
const syncSOAPEnvelopeVersion = load('syncSOAPEnvelopeVersion');
const detectXMLFileEncoding = load('detectXMLFileEncoding');

assert.deepStrictEqual(parseHeadersText('X-Trace: A:1\nAuthorization: Bearer abc'), {
  headers: { 'X-Trace': 'A:1', Authorization: 'Bearer abc' }, invalid: [],
});
assert.deepStrictEqual(parseHeadersText(`-H 'X-A: 1'\n--header "X-B: 2"`), {
  headers: { 'X-A': '1', 'X-B': '2' }, invalid: [],
});
assert.deepStrictEqual(parseHeadersText('{"X-A":1,"Enabled":true}'), {
  headers: { 'X-A': '1', Enabled: 'true' }, invalid: [],
});
assert.strictEqual(parseHeadersText('broken header').invalid.length, 1);

assert.strictEqual(
  syncXMLDeclaration('<?xml version="1.0" encoding="UTF-8"?><x/>', 'GB18030'),
  '<?xml version="1.0" encoding="GB18030"?><x/>'
);
assert.ok(syncSOAPEnvelopeVersion(
  '<Envelope xmlns="http://schemas.xmlsoap.org/soap/envelope/"/>', '1.2'
).includes('http://www.w3.org/2003/05/soap-envelope'));

assert.strictEqual(detectXMLFileEncoding(Uint8Array.from([0xEF, 0xBB, 0xBF, 0x3C])), 'utf-8');
assert.strictEqual(detectXMLFileEncoding(Uint8Array.from([0xFF, 0xFE, 0x3C, 0x00])), 'utf-16le');
assert.strictEqual(detectXMLFileEncoding(Buffer.from('<?xml version="1.0" encoding="GBK"?>')), 'gbk');

console.log('webservice page helpers: ok');
