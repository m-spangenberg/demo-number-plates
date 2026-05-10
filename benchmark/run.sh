#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BENCH_DIR="$ROOT_DIR/benchmark"
RESULTS_DIR="$BENCH_DIR/results"

SCENARIO="${SCENARIO:-mixed}"
DURATION="${DURATION:-30m}"
RATE="${RATE:-120}"
BASE_URL="${BASE_URL:-http://localhost:8081}"
API_KEY="${API_KEY:-bench-key}"
PREALLOCATED_VUS="${PREALLOCATED_VUS:-50}"
MAX_VUS="${MAX_VUS:-500}"
START_STACK="${START_STACK:-1}"
KEEP_STACK_UP="${KEEP_STACK_UP:-1}"

usage() {
  cat <<'EOF'
Usage: benchmark/run.sh [scenario]

Environment variables:
  SCENARIO           Scenario name: mixed, rest-standard, rest-vanity, graphql-standard
  DURATION           Benchmark duration, default 30m
  RATE               Target request rate per second
  BASE_URL           Base URL for the API, default http://localhost:8081
  API_KEY            API key, default bench-key
  PREALLOCATED_VUS   k6 pre-allocated VUs, default 50
  MAX_VUS            k6 max VUs, default 500
  START_STACK        1 to bring up constrained compose stack, 0 to reuse an existing one
  KEEP_STACK_UP      1 to leave stack running after test, 0 to stop it
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

if [[ $# -ge 1 ]]; then
  SCENARIO="$1"
fi

case "$SCENARIO" in
  mixed) SCENARIO_FILE="mixed.js" ;;
  rest-standard) SCENARIO_FILE="rest-standard.js" ;;
  rest-vanity) SCENARIO_FILE="rest-vanity.js" ;;
  graphql-standard) SCENARIO_FILE="graphql-standard.js" ;;
  *)
    echo "Unknown scenario: $SCENARIO" >&2
    usage >&2
    exit 1
    ;;
esac

timestamp="$(date +%Y%m%d-%H%M%S)"
RUN_DIR="$RESULTS_DIR/$timestamp-$SCENARIO"
mkdir -p "$RUN_DIR"
SUMMARY_JSON="$RUN_DIR/summary.json"

COMPOSE_CMD=(docker compose -f "$ROOT_DIR/docker-compose.yml" -f "$BENCH_DIR/docker-compose.bench.yml")

cleanup() {
  if [[ -n "${STATS_PID:-}" ]]; then
    kill "$STATS_PID" >/dev/null 2>&1 || true
  fi
  if [[ "$KEEP_STACK_UP" == "0" && "$START_STACK" == "1" ]]; then
    "${COMPOSE_CMD[@]}" down >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

if [[ "$START_STACK" == "1" ]]; then
  echo "Starting constrained benchmark stack..."
  "${COMPOSE_CMD[@]}" up -d --build
fi

echo "Waiting for readiness..."
until curl -fsS "$BASE_URL/readyz" | grep -q '"status":"ready"'; do
  sleep 5
done

echo "Sampling container stats to $RUN_DIR/container-stats.csv"
{
  echo "timestamp,name,cpu,memory,net_io,block_io"
  while true; do
    while IFS='|' read -r name cpu memory net_io block_io; do
      printf '%s,%s,%s,%s,%s,%s\n' "$(date -Iseconds)" "$name" "$cpu" "$memory" "$net_io" "$block_io"
    done < <(docker stats --no-stream --format '{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}|{{.NetIO}}|{{.BlockIO}}' go-api postgres redis)
    sleep 5
  done
} > "$RUN_DIR/container-stats.csv" &
STATS_PID=$!

echo "Running k6 scenario '$SCENARIO' for $DURATION at $RATE req/s..."
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --network host \
  -v "$BENCH_DIR:/benchmark" \
  -v "$RUN_DIR:/results" \
  -e BASE_URL="$BASE_URL" \
  -e API_KEY="$API_KEY" \
  -e DURATION="$DURATION" \
  -e RATE="$RATE" \
  -e PREALLOCATED_VUS="$PREALLOCATED_VUS" \
  -e MAX_VUS="$MAX_VUS" \
  grafana/k6:latest run \
  "/benchmark/scenarios/$SCENARIO_FILE" \
  --summary-export "/results/summary.json"

kill "$STATS_PID" >/dev/null 2>&1 || true
wait "$STATS_PID" 2>/dev/null || true
unset STATS_PID

if [[ ! -s "$SUMMARY_JSON" ]]; then
  echo "Benchmark summary was not written to $SUMMARY_JSON" >&2
  exit 1
fi

echo "Generating Markdown report..."
python3 "$BENCH_DIR/generate_report.py" \
  --summary-json "$SUMMARY_JSON" \
  --stats-csv "$RUN_DIR/container-stats.csv" \
  --output "$RUN_DIR/report.md" \
  --scenario "$SCENARIO" \
  --duration "$DURATION" \
  --rate "$RATE" \
  --base-url "$BASE_URL"

echo "Benchmark complete."
echo "Summary JSON: $SUMMARY_JSON"
echo "Stats CSV:    $RUN_DIR/container-stats.csv"
echo "Report:       $RUN_DIR/report.md"
