#!/usr/bin/env bash
# Smoke test helper — auto-kill zombie port, start server, run basic checks.
#
# Usage:
#   ./scripts/smoke.sh [PORT] [HEALTH_PATH]
#
# Defaults: PORT=18082, HEALTH_PATH=/healthz/liveness
#
# Requires:
#   - .env.local at repo root/backend (source'd automatically)
#   - psql + curl in PATH
#   - Docker PG running on localhost:15433 (etix_flash_dev)

set -euo pipefail

PORT="${1:-18082}"
HEALTH_PATH="${2:-/healthz/liveness}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

echo "=== Smoke: port=$PORT health=$HEALTH_PATH ==="
echo "REPO_ROOT=$REPO_ROOT"

# --- 1. Kill zombie on target port ---
echo ""
echo "--- 1. Killing zombie on :$PORT ---"
ZOMBIE=$(ss -tlnp 2>/dev/null | grep ":$PORT" | grep -oP 'pid=\K[0-9]+' | head -1 || true)
if [ -n "$ZOMBIE" ]; then
  echo "  found PID $ZOMBIE → killing"
  kill "$ZOMBIE" 2>/dev/null || true
  sleep 1
else
  echo "  no zombie"
fi

if ss -tlnp 2>/dev/null | grep -q ":$PORT"; then
  echo "  WARN: port still occupied after kill"
  exit 1
fi
echo "  port free"

# --- 2. Load env ---
echo ""
echo "--- 2. Loading env ---"
if [ -f "$REPO_ROOT/backend/.env.local" ]; then
  set -a
  # shellcheck disable=SC1091
  source "$REPO_ROOT/backend/.env.local"
  set +a
  echo "  sourced backend/.env.local"
else
  echo "  WARN: no .env.local — using defaults"
fi

# --- 3. Start server ---
echo ""
echo "--- 3. Starting server ---"
cd "$REPO_ROOT/backend"
PORT="$PORT" go run ./cmd/api > /tmp/smoke-server.log 2>&1 &
PID=$!
echo "  PID=$PID"

cleanup() {
  kill "$PID" 2>/dev/null || true
  wait "$PID" 2>/dev/null || true
  echo "  server killed"
}
trap cleanup EXIT

# Wait up to 10s for readiness
for i in $(seq 1 10); do
  if curl -sf "http://localhost:$PORT$HEALTH_PATH" > /dev/null 2>&1; then
    echo "  ready after ${i}s"
    break
  fi
  if [ "$i" -eq 10 ]; then
    echo "  FAIL: server not ready after 10s"
    tail -20 /tmp/smoke-server.log
    exit 1
  fi
  sleep 1
done

# --- 4. Smoke checks ---
echo ""
echo "--- 4. Smoke checks ---"
echo "  GET $HEALTH_PATH:"
curl -s -w "    HTTP=%{http_code}\n" "http://localhost:$PORT$HEALTH_PATH" | tail -2

echo ""
echo "  GET /events (public, no rate limit hit):"
curl -s -o /dev/null -w "    HTTP=%{http_code}\n" "http://localhost:$PORT/events"

echo ""
echo "--- 5. Server log tail ---"
tail -3 /tmp/smoke-server.log

echo ""
echo "=== Smoke PASS ==="
