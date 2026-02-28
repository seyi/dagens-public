// health_scenario.js - k6 load test for /health endpoint
// This is a lightweight test for baseline performance and availability validation
// Usage: k6 run --vus 100 --duration 1m health_scenario.js

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

// Custom metrics
const healthSuccessRate = new Rate('health_success_rate');
const healthDuration = new Trend('health_duration', true);

// Configuration
const BASE_URL = __ENV.BASE_URL || 'http://127.0.0.1:8080';

// Test options - health endpoint should handle very high load
export const options = {
    vus: __ENV.VUS ? parseInt(__ENV.VUS) : 100,
    duration: __ENV.DURATION || '1m',

    thresholds: {
        'http_req_failed': ['rate<0.001'],    // 0.1% error rate
        'http_req_duration': ['p(95)<100'],   // 95th percentile under 100ms
        'http_req_duration': ['p(99)<200'],   // 99th percentile under 200ms
        'health_success_rate': ['rate>0.999'], // 99.9% success rate
    },
};

// Main test function
export default function () {
    const startTime = Date.now();
    const res = http.get(`${BASE_URL}/health`);
    const duration = Date.now() - startTime;

    healthDuration.add(duration);

    const success = check(res, {
        'status is 200': (r) => r.status === 200,
        'response is healthy': (r) => {
            try {
                const body = JSON.parse(r.body);
                return body.status === 'healthy';
            } catch (e) {
                return false;
            }
        },
        'response time under 100ms': (r) => r.timings.duration < 100,
    });

    healthSuccessRate.add(success ? 1 : 0);

    // Minimal think time for health checks
    sleep(0.01);  // 10ms
}

// Setup
export function setup() {
    console.log(`Starting health endpoint load test against ${BASE_URL}`);
    return { startTime: new Date().toISOString() };
}

// Teardown
export function teardown(data) {
    console.log(`Health load test completed. Started at: ${data.startTime}`);
}
