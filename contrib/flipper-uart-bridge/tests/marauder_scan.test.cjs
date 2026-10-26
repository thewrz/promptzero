const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const test = require('node:test');
const path = require('node:path');

const script = fs.readFileSync(path.join(__dirname, '../marauder_scan.js'), 'utf8');

function runFallback(responsesByCommand) {
  let elapsed = 0;
  let activeCommand = '<startup>';
  let activeChunks = [];
  let ended = false;
  const writes = [];
  const output = [];
  const readCalls = [];
  const commandCounts = Object.create(null);
  const serial = {
    setup: () => {},
    readAny: (timeout) => {
      readCalls.push({ command: activeCommand, timeout });
      return activeChunks.shift();
    },
    write: (wireCommand) => {
      const command = wireCommand.trim();
      writes.push({ command, time: elapsed });
      activeCommand = command || '<newline>';
      const occurrence = commandCounts[command] || 0;
      commandCounts[command] = occurrence + 1;
      const commandResponses = responsesByCommand[command] || [];
      activeChunks = (commandResponses[occurrence] || []).slice();
    },
    end: () => { ended = true; },
  };

  vm.runInNewContext(script, {
    require: (name) => { assert.equal(name, 'serial'); return serial; },
    delay: (milliseconds) => { elapsed += milliseconds; },
    print: (value) => { output.push(String(value)); },
  }, { timeout: 1000 });

  return { writes, output, readCalls, ended, elapsed };
}

function commandNames(result) {
  return result.writes.map((entry) => entry.command);
}

const detail = (index) => `#info -a ${index}\r\nESSID: TEST_AP_${index}\r\nRSSI: -42\r\n>`;

test('fallback stops prior work, assembles list/details, and queries only listed AP indexes', () => {
  const listParts = [
    '#list -a\r\n[0][CH:',
    '6] TEST_AP_0 -40\r\n[1][CH:11] 001122334455 -50\r\n[2][CH:1] TEST_AP_2 -60\r\n',
    '[3][CH:3] TEST_AP_3 -70\r\n3 selected\r\n>',
  ];
  const result = runFallback({
    stopscan: [[], ['Stopping scan\r\n>']],
    'list -a': [listParts],
    'info -a 0': [['#info -a 0\r\nESSID: TEST_', 'AP_0\r\nRSSI: -40\r\n>']],
    'info -a 1': [[detail(1)]],
    'info -a 2': [[detail(2)]],
    'info -a 3': [[detail(3)]],
  });
  const commands = commandNames(result);
  const preflightStop = commands.indexOf('stopscan');
  const scanStart = commands.indexOf('scanall');
  const scanStop = commands.indexOf('stopscan', preflightStop + 1);
  const list = commands.indexOf('list -a');
  const infoCommands = commands.filter((command) => command.startsWith('info -a '));

  assert.ok(preflightStop >= 0 && preflightStop < scanStart, 'preflight stopscan must precede scanall');
  assert.ok(scanStop > scanStart && list > scanStop, 'the new scan must stop before listing APs');
  assert.ok(result.writes[scanStop].time - result.writes[scanStart].time >= 5000);
  assert.deepEqual(infoCommands, ['info -a 0', 'info -a 1', 'info -a 2', 'info -a 3']);
  assert.ok(!infoCommands.includes('info -a 4'), 'must not query an index absent from list -a');
  assert.ok(result.output.includes('#info -a 0\r\nESSID: TEST_AP_0\r\nRSSI: -40\r\n>'));
  assert.ok(result.ended, 'must release serial ownership');
});

test('zero AP list does not send any info command', () => {
  const result = runFallback({
    stopscan: [[], []],
    'list -a': [['#list -a\r\n0 selected\r\n>']],
  });

  assert.equal(commandNames(result).filter((command) => command.startsWith('info -a ')).length, 0);
  assert.ok(result.output.includes('No APs found; no details queried.'));
  assert.ok(result.ended);
});

test('malformed AP list reports failure and never queries details', () => {
  const result = runFallback({
    stopscan: [[], []],
    'list -a': [['#list -a\r\nunexpected response\r\n>']],
  });

  assert.ok(result.output.includes('ERROR: malformed list -a response'));
  assert.equal(commandNames(result).filter((command) => command.startsWith('info -a ')).length, 0);
  assert.ok(result.ended);
});

test('truncated list response fails closed within the response buffer bound', () => {
  const result = runFallback({
    stopscan: [[], []],
    'list -a': [['x'.repeat(5000)]],
  });

  assert.ok(result.output.includes('ERROR: list -a response exceeded the buffer limit'));
  assert.equal(commandNames(result).filter((command) => command.startsWith('info -a ')).length, 0);
  assert.equal(result.readCalls.filter((call) => call.command === 'list -a').length, 1);
  assert.ok(result.ended);
});

test('list response that consumes the read budget without going quiet fails closed', () => {
  const result = runFallback({
    stopscan: [[], []],
    'list -a': [Array.from({ length: 12 }, () => 'partial')],
  });

  assert.ok(result.output.includes('ERROR: list -a response exceeded the buffer limit'));
  assert.equal(result.readCalls.filter((call) => call.command === 'list -a').length, 12);
  assert.equal(commandNames(result).filter((command) => command.startsWith('info -a ')).length, 0);
  assert.ok(result.ended);
});

test('detail query count is capped at eleven actual list indexes', () => {
  const rows = [];
  const responses = { stopscan: [[], []], 'list -a': [[`#list -a\r\n${Array.from(
    { length: 13 }, (_, index) => `[${index}][CH:6] TEST_AP_${index} -40`,
  ).join('\r\n')}\r\n13 selected\r\n>`]] };
  for(let index = 0; index < 13; index++) {
    rows.push(`info -a ${index}`);
    responses[`info -a ${index}`] = [[detail(index)]];
  }
  const result = runFallback(responses);
  const infoCommands = commandNames(result).filter((command) => command.startsWith('info -a '));

  assert.equal(infoCommands.length, 11);
  assert.deepEqual(infoCommands, rows.slice(0, 11));
  assert.ok(result.output.includes('Details limited to the first 11 listed APs.'));
});
