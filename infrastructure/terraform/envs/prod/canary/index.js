'use strict';

const MAX_RESPONSE_BYTES = 1024 * 1024;

function parseMinimumItemTypes(value) {
  if (!/^[1-9][0-9]*$/.test(value)) {
    throw new Error('MIN_ITEM_TYPES must be a positive integer');
  }
  const minimum = Number.parseInt(value, 10);
  if (!Number.isSafeInteger(minimum) || minimum < 1) {
    throw new Error('MIN_ITEM_TYPES must be a positive integer');
  }
  return minimum;
}

function validateItemTypes(statusCode, rawBody, minimum) {
  if (statusCode < 200 || statusCode > 299) {
    throw new Error(`golden journey returned HTTP ${statusCode}`);
  }

  let document;
  try {
    document = JSON.parse(rawBody);
  } catch {
    throw new Error('golden journey returned malformed JSON');
  }

  const observed = Array.isArray(document.itemTypes) ? document.itemTypes.length : 0;
  if (observed < minimum) {
    throw new Error(`golden journey itemTypes count ${observed} is below expected minimum ${minimum}`);
  }
  return observed;
}

async function readResponse(response) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    response.on('data', (chunk) => {
      size += chunk.length;
      if (size > MAX_RESPONSE_BYTES) {
        reject(new Error('golden journey response exceeded 1 MiB'));
        response.destroy();
        return;
      }
      chunks.push(chunk);
    });
    response.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    response.on('error', reject);
  });
}

async function runCanary({
  synthetics = require('@aws/synthetics-core'),
  targetURL = process.env.TARGET_URL,
  minimumText = process.env.MIN_ITEM_TYPES || '1',
} = {}) {
  const target = new URL(targetURL);
  if (target.protocol !== 'http:' && target.protocol !== 'https:') {
    throw new Error('TARGET_URL must use http or https');
  }
  const minimum = parseMinimumItemTypes(minimumText);
  const requestOptions = {
    protocol: target.protocol,
    hostname: target.hostname,
    port: target.port || undefined,
    path: `${target.pathname}${target.search}`,
    method: 'GET',
    headers: {
      'User-Agent': synthetics.getCanaryUserAgentString(),
      Accept: 'application/json',
    },
  };
  const validateResponse = async (response) => {
    const body = await readResponse(response);
    validateItemTypes(response.statusCode, body, minimum);
  };
  const stepConfig = {
    includeRequestHeaders: false,
    includeResponseHeaders: false,
    includeRequestBody: false,
    includeResponseBody: false,
  };

  await synthetics.executeHttpStep('item-types', requestOptions, validateResponse, stepConfig);
}

exports.handler = async () => runCanary();
exports.parseMinimumItemTypes = parseMinimumItemTypes;
exports.validateItemTypes = validateItemTypes;
exports.runCanary = runCanary;
