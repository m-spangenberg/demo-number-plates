import http from 'k6/http';
import { check } from 'k6';

const baseUrl = __ENV.BASE_URL || 'http://localhost:8081';
const apiKey = __ENV.API_KEY || 'bench-key';

const standardHits = ['432069W', '243287W', '101544HB', '928976OJ', '128715BK'];
const standardMisses = ['987654ZZZ', '345678XYZ', '777777QQQ'];
const vanityHits = ['emyds', 'muntjak', 'howdied', 'hastati', 'martnet'];
const vanityMisses = ['q9x7z2p', 'zv8m1rk', 'h2p4q7x'];

export function buildOptions(defaultRate) {
  return {
    scenarios: {
      steady: {
        executor: 'constant-arrival-rate',
        rate: Number(__ENV.RATE || defaultRate),
        timeUnit: '1s',
        duration: __ENV.DURATION || '30m',
        preAllocatedVUs: Number(__ENV.PREALLOCATED_VUS || 50),
        maxVUs: Number(__ENV.MAX_VUS || 500),
      },
    },
    thresholds: {
      http_req_failed: ['rate<0.01'],
      http_req_duration: ['p(95)<150', 'p(99)<300'],
    },
    summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max', 'count'],
  };
}

export function get(url) {
  const response = http.get(url, {
    headers: {
      'X-API-Key': apiKey,
    },
  });
  check(response, {
    'status is 200': (res) => res.status === 200,
  });
  return response;
}

export function postGraphQL(query) {
  const response = http.post(
    `${baseUrl}/graphql`,
    JSON.stringify({ query }),
    {
      headers: {
        'Content-Type': 'application/json',
        'X-API-Key': apiKey,
      },
    }
  );
  check(response, {
    'status is 200': (res) => res.status === 200,
  });
  return response;
}

export function standardQuery(hitRate = 0.8) {
  const plate = pickValue(standardHits, standardMisses, hitRate);
  return `${baseUrl}/find?t=std&q=${plate}`;
}

export function vanityQuery(hitRate = 0.8) {
  const plate = pickValue(vanityHits, vanityMisses, hitRate);
  return `${baseUrl}/find?t=vty&q=${plate}`;
}

export function graphqlStandardQuery(hitRate = 0.8) {
  const plate = pickValue(standardHits, standardMisses, hitRate);
  return `query { lookupPlate(type: "std", query: "${plate}") { input type normalized available } }`;
}

export function graphqlVanityQuery(hitRate = 0.8) {
  const plate = pickValue(vanityHits, vanityMisses, hitRate);
  return `query { lookupPlate(type: "vty", query: "${plate}") { input type normalized available suggestions } }`;
}

export function graphqlVanitySuggestionQuery() {
  return 'query { suggestVanity(prefix: "EMY", limit: 5) }';
}

function pickValue(hitValues, missValues, hitRate) {
  const source = Math.random() < hitRate ? hitValues : missValues;
  const index = Math.floor(Math.random() * source.length);
  return source[index];
}
