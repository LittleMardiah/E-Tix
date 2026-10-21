#!/usr/bin/env bash
# FIX P9a-8 TD-F0-06: bootstrap a fresh dev database.
#
# Starts local dependencies (unless --no-docker), applies all migrations, and
# loads the deterministic test/seed fixtures. Safe to re-run (idempotent).
#
# Usage:
#   ./scripts/dev_setup.sh [--no-docker]
#
# Env:
#   DATABASE_URL  override target DB (default: local docker Postgres on 15433)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$(dirname "$SCRIPT_DIR")"
REPO_ROOT="$(dirname "$BACKEND_DIR")"

DB_URL="${DATABASE_URL:-postgresql://postgres:postgres@localhost:15433/etix_flash_dev?sslmode=disable}"
START_DOCKER=1
if [ "${1:-}" = "--no-docker" ]; then
  START_DOCKER=0
fi

cd "$BACKEND_DIR"

echo "[dev_setup] repo=$REPO_ROOT"
echo "[dev_setup] database=$DB_URL"

if [ "$START_DOCKER" -eq 1 ] && command -v docker >/dev/null 2>&1; then
  echo "[dev_setup] starting docker compose services..."
  (cd "$REPO_ROOT" && docker compose up -d postgres redis rabbitmq)
else
  echo "[dev_setup] skipping docker compose (disabled or docker unavailable)"
fi

echo "[dev_setup] waiting for postgres..."
for _ in $(seq 1 30); do
  if command -v pg_isready >/dev/null 2>&1; then
    pg_isready -h localhost -p 15433 -U postgres >/dev/null 2>&1 && break
  elif command -v psql >/dev/null 2>&1; then
    psql "$DB_URL" -c 'SELECT 1' >/dev/null 2>&1 && break
  else
    echo "[dev_setup] neither pg_isready nor psql found; cannot verify readiness" >&2
    exit 1
  fi
  sleep 1
done

echo "[dev_setup] applying migrations..."
go run ./cmd/migrate -path ./migrations -database "$DB_URL" up

if [ -f "tests/fixtures/seed.sql" ]; then
  if command -v psql >/dev/null 2>&1; then
    echo "[dev_setup] loading seed fixtures..."
    psql "$DB_URL" -v ON_ERROR_STOP=1 -f tests/fixtures/seed.sql >/dev/null
  else
    echo "[dev_setup] WARN: psql not found; skipping seed fixtures" >&2
  fi
fi

echo "[dev_setup] DONE"
