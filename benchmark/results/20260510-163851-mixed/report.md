# Benchmark Report

Scenario: mixed
Duration: 5m
Target rate: 2000 req/s
Base URL: http://localhost:8081

## Request Summary

| Metric | Value |
|--------|-------|
| Total requests | 598343 |
| Average RPS | 1994.46 |
| Error rate | 0.0000% |
| Latency avg | 0.62 ms |
| Latency p50 | 0.45 ms |
| Latency p90 | 0.89 ms |
| Latency p95 | 1.02 ms |
| Latency p99 | 1.43 ms |
| Latency max | 82.88 ms |

## Container Peaks

| Container | CPU Peak | Memory Peak |
|-----------|----------|-------------|
| go-api | 69.92% | 12.4 MiB |
| postgres | 10.08% | 170.9 MiB |
| redis | 20.49% | 30.0 MiB |
