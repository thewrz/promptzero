const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const test = require('node:test');
const path = require('node:path');

test('fallback collects a fresh scan before reading bounded AP details', () => {
  let elapsed = 0;
  let ended = false;
  const writes = [];
  const serial = {
    setup: () => {},
    readAny: () => 'fixture response',
    write: (command) => writes.push({ command: command.trim(), time: elapsed }),
    end: () => { ended = true; },
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../marauder_scan.js'), 'utf8'), {
    require: (name) => { assert.equal(name, 'serial'); return serial; },
    delay: (ms) => { elapsed += ms; },
    print: () => {},
  }, { timeout: 1000 });
  const start = writes.findIndex((entry) => entry.command === 'scanall');
  const stop = writes.findIndex((entry) => entry.command === 'stopscan');
  const list = writes.findIndex((entry) => entry.command === 'list -a');
  assert.ok(start >= 0, 'must start a fresh scan');
  assert.ok(stop > start && list > stop, 'must stop before reading results');
  assert.ok(writes[stop].time - writes[start].time >= 5000);
  const details = writes.filter((entry) => entry.command.startsWith('info -a '));
  assert.equal(details.length, 11);
  assert.ok(ended, 'must release serial ownership');
});
