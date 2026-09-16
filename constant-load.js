import http from 'k6/http';
import { check } from 'k6';

const RATE = Number(__ENV.RATE || 5);
const BASE_URL = __ENV.BASE_URL || 'http://3.89.55.234:8080';
const ITERATIONS = Number(__ENV.ITERATIONS || 500000);

export const options = {
  scenarios: {
    fixed_rate: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: '3m',
      preAllocatedVUs: 50,
      maxVUs: 1000,
    },
  },
};

export default function () {
  const res = http.get(`${BASE_URL}/work?iterations=${ITERATIONS}`);
  
  check(res, {
    'status 200': (r) => r.status === 200,
  });
}