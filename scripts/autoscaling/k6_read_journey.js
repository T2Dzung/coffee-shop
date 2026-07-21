import http from 'k6/http';
import { check } from 'k6';
import { Rate } from 'k6/metrics';

// Custom rate metric for semantic checks gate
const successfulChecks = new Rate('successful_checks');

// Read configuration from environment variables with safe defaults
const BASE_URL = __ENV.BASE_URL || 'http://localhost/api';
const TARGET_RATE = parseInt(__ENV.RATE || '10', 10);
const TIME_UNIT = __ENV.TIME_UNIT || '1s';
const DURATION = __ENV.DURATION || '1m';
const PRE_ALLOCATED_VUS = parseInt(__ENV.PRE_ALLOCATED_VUS || '10', 10);
const MAX_VUS = parseInt(__ENV.MAX_VUS || '50', 10);

export const options = {
  scenarios: {
    read_journey_constant_rate: {
      executor: 'constant-arrival-rate',
      rate: TARGET_RATE,
      timeUnit: TIME_UNIT,
      duration: DURATION,
      preAllocatedVUs: PRE_ALLOCATED_VUS,
      maxVUs: MAX_VUS,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'], // < 1% HTTP network/status failure rate
    http_req_duration: ['p(50)<200', 'p(95)<500', 'p(99)<1000'], // Latency SLAs for p50, p95, p99
    successful_checks: ['rate==1.0'], // 100% semantic checks must pass (Strict Pass/Fail Gate)
    dropped_iterations: ['count==0'], // Zero dropped iterations allowed from generator/SUT bottleneck
  },
};

const TARGET_PATH = __ENV.TARGET_PATH || '/v1/api/item-types';

export default function () {
  const url = `${BASE_URL}${TARGET_PATH}`;
  const params = {
    headers: {
      'Accept': TARGET_PATH === '/' ? 'text/html,application/xhtml+xml' : 'application/json',
      'User-Agent': 'k6-DEV3-autoscaling-harness/1.0',
    },
  };

  const res = http.get(url, params);

  let checkResult;
  if (TARGET_PATH === '/') {
    checkResult = check(res, {
      'status is 200': (r) => r.status === 200,
    });
  } else {
    checkResult = check(res, {
      'status is 200': (r) => r.status === 200,
      'content-type is application/json': (r) => r.headers['Content-Type'] && r.headers['Content-Type'].includes('application/json'),
      'response body contains itemTypes array': (r) => {
        try {
          const data = JSON.parse(r.body);
          const items = data.itemTypes || data.item_types || data;
          return Array.isArray(items) && items.length > 0;
        } catch (e) {
          return false;
        }
      },
    });
  }

  successfulChecks.add(checkResult);
}
