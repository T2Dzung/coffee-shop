'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const { EventEmitter } = require('node:events');
const {
  loadSynthetics,
  parseMinimumItemTypes,
  runCanary,
  validateItemTypes,
} = require('./index');

test('loadSynthetics resolves the named runtime export and rejects the flat module shape', () => {
  const client = { executeHttpStep: async () => {} };
  assert.equal(loadSynthetics({ synthetics: client }), client);
  assert.throws(
    () => loadSynthetics({ executeHttpStep: async () => {} }),
    /must expose synthetics\.executeHttpStep/,
  );
});

test('validateItemTypes accepts a non-empty successful document', () => {
  assert.equal(validateItemTypes(200, '{"itemTypes":[{"type":0}]}', 1), 1);
});

test('validateItemTypes rejects status, malformed JSON, and insufficient items without logging payloads', () => {
  assert.throws(() => validateItemTypes(503, '{}', 1), /HTTP 503/);
  assert.throws(() => validateItemTypes(200, 'secret-body', 1), /malformed JSON/);
  assert.throws(() => validateItemTypes(200, '{"itemTypes":[]}', 1), /count 0.*minimum 1/);
});

test('parseMinimumItemTypes rejects unsafe controlled-negative inputs', () => {
  assert.equal(parseMinimumItemTypes('11'), 11);
  for (const value of ['', '0', '-1', '1-item', 'not-a-number']) {
    assert.throws(() => parseMinimumItemTypes(value), /positive integer/);
  }
});

test('runCanary configures one GET step and validates the response body', async () => {
  let observed;
  const synthetics = {
    executeHttpStep: async (name, options, callback, config) => {
      observed = { name, options, config };
      const response = new EventEmitter();
      response.statusCode = 200;
      const validation = callback(response);
      response.emit('data', Buffer.from('{"itemTypes":[{"type":0}]}'));
      response.emit('end');
      await validation;
    },
  };

  await runCanary({
    synthetics,
    targetURL: 'http://example.test/api/v1/api/item-types',
    minimumText: '1',
  });

  assert.equal(observed.name, 'item-types');
  assert.equal(observed.options.method, 'GET');
  assert.equal(observed.options.hostname, 'example.test');
  assert.equal(observed.options.path, '/api/v1/api/item-types');
  assert.equal(observed.options.headers['User-Agent'], 'CoffeeShop-O2-Synthetic/1.0');
  assert.equal(observed.config.includeResponseBody, false);
});

test('runCanary propagates transport and timeout failures', async () => {
  const synthetics = {
    executeHttpStep: async () => {
      throw new Error('request timed out');
    },
  };

  await assert.rejects(
    runCanary({
      synthetics,
      targetURL: 'http://example.test/api/v1/api/item-types',
      minimumText: '1',
    }),
    /timed out/,
  );
});
