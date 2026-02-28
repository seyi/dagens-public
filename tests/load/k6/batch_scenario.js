// batch_scenario.js - k6 load test for /api/v1/agents/batch_execute endpoint
// Usage: k6 run --vus 10 --duration 2m batch_scenario.js
// Or with env vars: VUS=50 DURATION=5m BASE_URL=http://localhost:8080 k6 run batch_scenario.js

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';

// Custom metrics
const batchErrors = new Counter('batch_errors');
const batchSuccessRate = new Rate('batch_success_rate');
const batchDuration = new Trend('batch_duration', true);
const requestsPerBatch = new Trend('requests_per_batch');

// Configuration from environment variables
const BASE_URL = __ENV.BASE_URL || 'http://127.0.0.1:8080';
const BATCH_SIZE_MIN = __ENV.BATCH_SIZE_MIN ? parseInt(__ENV.BATCH_SIZE_MIN) : 3;
const BATCH_SIZE_MAX = __ENV.BATCH_SIZE_MAX ? parseInt(__ENV.BATCH_SIZE_MAX) : 10;

// Test options
export const options = {
    vus: __ENV.VUS ? parseInt(__ENV.VUS) : 10,
    duration: __ENV.DURATION || '2m',

    thresholds: {
        'http_req_failed': ['rate<0.01'],
        'http_req_duration': ['p(95)<1000'],  // Higher threshold for batch (1s)
        'batch_success_rate': ['rate>0.99'],
    },
};

// Test data
const agents = ['echo', 'summarizer', 'classifier', 'sentiment'];

const sampleInstructions = [
    'Hello world',
    'The quick brown fox',
    'This is excellent',
    'Testing batch execution',
    'Load test input',
    'Performance validation',
    'Good product',
    'Bad experience',
    'Neutral statement',
    'Amazing wonderful great',
];

// Get random integer between min and max (inclusive)
function getRandomInt(min, max) {
    return Math.floor(Math.random() * (max - min + 1)) + min;
}

// Get random item from array
function getRandomItem(arr) {
    return arr[Math.floor(Math.random() * arr.length)];
}

// Generate a batch of requests
function generateBatch(size) {
    const requests = [];
    for (let i = 0; i < size; i++) {
        requests.push({
            agent_id: getRandomItem(agents),
            instruction: getRandomItem(sampleInstructions),
            context: {},
        });
    }
    return requests;
}

// Main test function
export default function () {
    // Generate random batch size
    const batchSize = getRandomInt(BATCH_SIZE_MIN, BATCH_SIZE_MAX);
    requestsPerBatch.add(batchSize);

    // Generate batch payload
    const payload = JSON.stringify({
        requests: generateBatch(batchSize),
    });

    const params = {
        headers: {
            'Content-Type': 'application/json',
        },
        tags: {
            batch_size: batchSize.toString(),
        },
    };

    // Execute batch request
    const startTime = Date.now();
    const res = http.post(`${BASE_URL}/api/v1/agents/batch_execute`, payload, params);
    const duration = Date.now() - startTime;

    batchDuration.add(duration);

    // Validate response
    const success = check(res, {
        'status is 200': (r) => r.status === 200,
        'response has results array': (r) => {
            try {
                const body = JSON.parse(r.body);
                return Array.isArray(body.results);
            } catch (e) {
                return false;
            }
        },
        'results count matches request count': (r) => {
            try {
                const body = JSON.parse(r.body);
                return body.results && body.results.length === batchSize;
            } catch (e) {
                return false;
            }
        },
        'all results successful': (r) => {
            try {
                const body = JSON.parse(r.body);
                return body.results && body.results.every(result => result.success === true);
            } catch (e) {
                return false;
            }
        },
    });

    if (success) {
        batchSuccessRate.add(1);
    } else {
        batchSuccessRate.add(0);
        batchErrors.add(1);
    }

    // Longer think time for batch operations
    sleep(0.2 + Math.random() * 0.3);  // 200-500ms
}

// Setup function
export function setup() {
    const healthRes = http.get(`${BASE_URL}/health`);

    if (healthRes.status !== 200) {
        throw new Error(`Server health check failed: ${healthRes.status}`);
    }

    console.log(`Starting batch load test against ${BASE_URL}`);
    console.log(`Batch sizes: ${BATCH_SIZE_MIN} - ${BATCH_SIZE_MAX}`);

    return {
        startTime: new Date().toISOString(),
        baseUrl: BASE_URL,
    };
}

// Teardown function
export function teardown(data) {
    console.log(`Batch load test completed. Started at: ${data.startTime}`);
}
