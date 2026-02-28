// execute_scenario.js - k6 load test for /api/v1/agents/execute endpoint
// Usage: k6 run --vus 10 --duration 2m execute_scenario.js
// Or with env vars: VUS=50 DURATION=5m BASE_URL=http://localhost:8080 k6 run execute_scenario.js

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';

// Custom metrics
const executeErrors = new Counter('execute_errors');
const executeSuccessRate = new Rate('execute_success_rate');
const executeDuration = new Trend('execute_duration', true);

// Configuration from environment variables
const BASE_URL = __ENV.BASE_URL || 'http://127.0.0.1:8080';
const AGENTS = (__ENV.AGENTS || 'echo,summarizer,classifier,sentiment').split(',');

// Test options
export const options = {
    // VUs and duration can be overridden via CLI or env
    vus: __ENV.VUS ? parseInt(__ENV.VUS) : 10,
    duration: __ENV.DURATION || '2m',

    // Thresholds for pass/fail criteria
    thresholds: {
        // Error rate should be less than 1%
        'http_req_failed': ['rate<0.01'],
        // 95th percentile response time should be under 500ms
        'http_req_duration': ['p(95)<500'],
        // Custom success rate should be above 99%
        'execute_success_rate': ['rate>0.99'],
    },

    // Stages for ramping (optional, use if not using constant VUs)
    // Uncomment to use staged load pattern
    // stages: [
    //     { duration: '30s', target: 10 },   // Ramp up to 10 VUs
    //     { duration: '1m', target: 50 },    // Ramp up to 50 VUs
    //     { duration: '2m', target: 50 },    // Stay at 50 VUs
    //     { duration: '30s', target: 0 },    // Ramp down
    // ],
};

// Test data - various instructions for different agents
const testInstructions = {
    'echo': [
        'Hello, World!',
        'Testing the echo agent',
        'Load test in progress',
        'k6 performance validation',
    ],
    'summarizer': [
        'The quick brown fox jumps over the lazy dog',
        'This is a test sentence with multiple words for summarization',
        'One two three four five six seven eight nine ten',
        'Load testing the summarizer agent with various inputs',
    ],
    'classifier': [
        'This product is excellent and amazing!',
        'The service was terrible and disappointing',
        'It was okay, nothing special',
        'I love this great wonderful product',
    ],
    'sentiment': [
        'I absolutely love this amazing product!',
        'This is the worst experience ever, horrible',
        'The item arrived on time and works fine',
        'Best purchase I have ever made, excellent quality',
    ],
};

// Get random item from array
function getRandomItem(arr) {
    return arr[Math.floor(Math.random() * arr.length)];
}

// Main test function - executed by each VU
export default function () {
    // Select random agent
    const agent = getRandomItem(AGENTS);

    // Select random instruction for the agent
    const instructions = testInstructions[agent] || testInstructions['echo'];
    const instruction = getRandomItem(instructions);

    // Prepare request payload
    const payload = JSON.stringify({
        agent_id: agent,
        instruction: instruction,
        context: {},
    });

    const params = {
        headers: {
            'Content-Type': 'application/json',
        },
        tags: {
            agent: agent,  // Tag for filtering metrics by agent
        },
    };

    // Execute request
    const startTime = Date.now();
    const res = http.post(`${BASE_URL}/api/v1/agents/execute`, payload, params);
    const duration = Date.now() - startTime;

    // Record custom metrics
    executeDuration.add(duration);

    // Validate response
    const success = check(res, {
        'status is 200': (r) => r.status === 200,
        'response has success field': (r) => {
            try {
                const body = JSON.parse(r.body);
                return body.success === true;
            } catch (e) {
                return false;
            }
        },
        'response has output': (r) => {
            try {
                const body = JSON.parse(r.body);
                return body.output !== undefined && body.output !== '';
            } catch (e) {
                return false;
            }
        },
        'response has metadata': (r) => {
            try {
                const body = JSON.parse(r.body);
                return body.metadata !== undefined;
            } catch (e) {
                return false;
            }
        },
    });

    // Update custom metrics
    if (success) {
        executeSuccessRate.add(1);
    } else {
        executeSuccessRate.add(0);
        executeErrors.add(1);
    }

    // Think time between requests (simulates real user behavior)
    sleep(0.1 + Math.random() * 0.1);  // 100-200ms
}

// Setup function - runs once before test
export function setup() {
    // Verify server is healthy before starting
    const healthRes = http.get(`${BASE_URL}/health`);

    if (healthRes.status !== 200) {
        throw new Error(`Server health check failed: ${healthRes.status}`);
    }

    console.log(`Starting load test against ${BASE_URL}`);
    console.log(`Testing agents: ${AGENTS.join(', ')}`);

    return {
        startTime: new Date().toISOString(),
        baseUrl: BASE_URL,
        agents: AGENTS,
    };
}

// Teardown function - runs once after test
export function teardown(data) {
    console.log(`Load test completed. Started at: ${data.startTime}`);
}
