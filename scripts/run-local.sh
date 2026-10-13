#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ -f .env.local ]; then set -a; . ./.env.local; set +a; fi
: "${DATABASE_URL:=postgres://argus:argus_password@localhost:5432/argus_ota?sslmode=disable}"
: "${REDIS_ADDR:=localhost:6379}"; : "${KAFKA_BROKERS:=localhost:9092}"; : "${KAFKA_TOPIC:=batch-events}"
export DATABASE_URL REDIS_ADDR KAFKA_BROKERS KAFKA_TOPIC
mkdir -p logs
pids=""
stop_services(){trap - EXIT INT TERM; for pid in $pids; do kill "$pid" 2>/dev/null || true; done; for pid in $pids; do wait "$pid" 2>/dev/null || true; done;}
trap stop_services EXIT INT TERM
processing_workers="parser-worker aggregator-worker"
case ${ARGUS_PROCESSING_MODE:-real} in
 real) ;;
 mock) processing_workers="mock-cpp-worker mock-python-worker" ;;
 *) printf 'ARGUS_PROCESSING_MODE must be real or mock\n' >&2; exit 1 ;;
esac
for service in ingestor orchestrator query-service $processing_workers event-relay ai-worker progress-sse; do
 "./build/$service" > "logs/$service.log" 2>&1 & pids="$pids $!"
done
printf 'Services started; HTTP upload :8080, query :8081, progress SSE :8090. Logs are in logs/.\n'
while :; do for pid in $pids; do if ! kill -0 "$pid" 2>/dev/null; then printf 'A service exited; see logs/.\n' >&2; exit 1; fi; done; sleep 1; done
