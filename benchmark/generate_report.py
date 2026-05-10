#!/usr/bin/env python3

import argparse
import csv
import json
from pathlib import Path


def parse_args():
    parser = argparse.ArgumentParser(description="Generate a Markdown benchmark report")
    parser.add_argument("--summary-json", required=True)
    parser.add_argument("--stats-csv", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--scenario", required=True)
    parser.add_argument("--duration", required=True)
    parser.add_argument("--rate", required=True)
    parser.add_argument("--base-url", required=True)
    return parser.parse_args()


def read_summary(path):
    with open(path, "r", encoding="utf-8") as handle:
        return json.load(handle)


def read_stats(path):
    peaks = {}
    with open(path, "r", encoding="utf-8") as handle:
        reader = csv.DictReader(handle)
        for row in reader:
            name = row["name"]
            cpu = parse_percent(row["cpu"])
            memory = parse_memory_usage(row["memory"])
            peak = peaks.setdefault(name, {"cpu": 0.0, "memory_bytes": 0.0})
            peak["cpu"] = max(peak["cpu"], cpu)
            peak["memory_bytes"] = max(peak["memory_bytes"], memory)
    return peaks


def parse_percent(value):
    value = value.strip().rstrip("%")
    return float(value) if value else 0.0


def parse_memory_usage(value):
    used = value.split("/")[0].strip()
    number = ""
    unit = ""
    for char in used:
        if char.isdigit() or char == ".":
            number += char
        else:
            unit += char
    if not number:
        return 0.0
    multiplier = {
        "B": 1,
        "kB": 1000,
        "KB": 1000,
        "MB": 1000**2,
        "GB": 1000**3,
        "KiB": 1024,
        "MiB": 1024**2,
        "GiB": 1024**3,
    }.get(unit.strip(), 1)
    return float(number) * multiplier


def format_bytes(num_bytes):
    units = ["B", "KiB", "MiB", "GiB"]
    value = float(num_bytes)
    unit = units[0]
    for unit in units:
        if value < 1024 or unit == units[-1]:
            break
        value /= 1024
    return f"{value:.1f} {unit}"


def metric(summary, name):
    return summary["metrics"].get(name, {})


def trend_value(metric_data, key):
    value = metric_data.get(key, 0)
    if isinstance(value, float):
        return f"{value:.2f}"
    return str(value)


def build_markdown(args, summary, peaks):
    reqs = metric(summary, "http_reqs")
    duration = metric(summary, "http_req_duration")
    failures = metric(summary, "http_req_failed")

    container_rows = []
    for container in ["go-api", "postgres", "redis"]:
        peak = peaks.get(container, {"cpu": 0.0, "memory_bytes": 0.0})
        container_rows.append(
            f"| {container} | {peak['cpu']:.2f}% | {format_bytes(peak['memory_bytes'])} |"
        )

    lines = [
        "# Benchmark Report",
        "",
        f"Scenario: {args.scenario}",
        f"Duration: {args.duration}",
        f"Target rate: {args.rate} req/s",
        f"Base URL: {args.base_url}",
        "",
        "## Request Summary",
        "",
        "| Metric | Value |",
        "|--------|-------|",
        f"| Total requests | {int(reqs.get('count', 0))} |",
        f"| Average RPS | {reqs.get('rate', 0):.2f} |",
        f"| Error rate | {failures.get('rate', 0) * 100:.4f}% |",
        f"| Latency avg | {trend_value(duration, 'avg')} ms |",
        f"| Latency p50 | {trend_value(duration, 'med')} ms |",
        f"| Latency p90 | {trend_value(duration, 'p(90)')} ms |",
        f"| Latency p95 | {trend_value(duration, 'p(95)')} ms |",
        f"| Latency p99 | {trend_value(duration, 'p(99)')} ms |",
        f"| Latency max | {trend_value(duration, 'max')} ms |",
        "",
        "## Container Peaks",
        "",
        "| Container | CPU Peak | Memory Peak |",
        "|-----------|----------|-------------|",
    ]
    lines.extend(container_rows)
    return "\n".join(lines)


def main():
    args = parse_args()
    summary = read_summary(args.summary_json)
    peaks = read_stats(args.stats_csv)
    markdown = build_markdown(args, summary, peaks)
    Path(args.output).write_text(markdown, encoding="utf-8")


if __name__ == "__main__":
    main()
