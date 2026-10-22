#!/usr/bin/env bash
# FASE 9 Epic B - Disaster Recovery Drills (DRP 7.2).
#
# Tabletop / dry-run simulation of DRP Playbook #1 (PostgreSQL primary
# failure / corruption), #2 (Redis cluster total failure) and #3 (RabbitMQ
# failure) against the local Docker stack (docker-compose.yml).
#
# No live staging environment (Render / Supabase) is available, so the drill
# runs against Docker locally, as allowed by DRP 7.2 ("No-Impact: gunakan
# staging environment atau dry-run").
#
# Usage:
#   ./scripts/dr_drill.sh [--playbook <1|2|3|all>] [--keep-db]
#
# Environment:
#   DR_REDIS_ADDR    Redis host:port       (default localhost:16379)
#   DR_AMQP_URL      RabbitMQ URL           (default amqp://guest:guest@localhost:5673/)
#   DR_DRILL_DB      drill database name    (default etix_flash_dr_<timestamp>)
#   DR_DATABASE_URL  drill database URL     (default postgresql://postgres:postgres@localhost:15433/<DR_DRILL_DB>?sslmode=disable)
#   DR_LOG_DIR       drill log directory    (default backend/logs)
#
# Safety:
#   * The development database (etix_flash_dev) is NEVER touched. The
#     Postgres corruption/restore drill runs inside a dedicated drill
#     database whose name carries the run timestamp.
#   * Every fault injection is undone by a cleanup trap, so an aborted drill
#     always leaves the Docker stack running.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$(dirname "$SCRIPT_DIR")"
REPO_ROOT="$(dirname "$BACKEND_DIR")"

STAMP="$(date '+%Y%m%d_%H%M%S')"

REDIS_ADDR="${DR_REDIS_ADDR:-localhost:16379}"
AMQP_URL="${DR_AMQP_URL:-amqp://guest:guest@localhost:5673/}"
DRILL_DB_EPHEMERAL=1
if [ -n "${DR_DRILL_DB:-}" ]; then
  DRILL_DB_EPHEMERAL=0
  DRILL_DB="$DR_DRILL_DB"
else
  DRILL_DB="etix_flash_dr_${STAMP}"
fi
DB_URL="${DR_DATABASE_URL:-postgresql://postgres:postgres@localhost:15433/${DRILL_DB}?sslmode=disable}"
LOG_DIR="${DR_LOG_DIR:-${BACKEND_DIR}/logs}"
LOG_FILE="${LOG_DIR}/dr_drill_${STAMP}.log"

PG_CONTAINER="${DR_PG_CONTAINER:-etix-postgres}"
REDIS_CONTAINER="${DR_REDIS_CONTAINER:-etix-redis}"
RABBIT_CONTAINER="${DR_RABBIT_CONTAINER:-etix-rabbitmq}"

CRITICAL_TABLES="users events sections bookings payment_obligations tickets ticket_scans resale_transactions admin_actions insights blacklist system_config"

PLAYBOOK="all"
KEEP_DB=0

RESULTS=()
FAILURES=0
REDIS_DOWN=0
RABBIT_DOWN=0
CLEANED=0
PREFLIGHT_OK=0
RTO_MEASURED=0

SNAPSHOT_FILE="${LOG_DIR}/dr_snapshot_${STAMP}.json"
CONTAINER_DUMP="/tmp/etix_dr_${STAMP}.dump"

usage() {
  cat <<'USAGE'
FASE 9 Epic B - Disaster Recovery Drills (DRP 7.2)

Usage:
  ./scripts/dr_drill.sh [--playbook <1|2|3|all>] [--keep-db]

Options:
  --playbook <n>  Run a single playbook (1 = Postgres restore, 2 = Redis,
                   3 = RabbitMQ). Default: all.
  --keep-db       Keep the drill database after the run (default: dropped).
  -h, --help      Show this help.
USAGE
}

# say prints a line to the terminal and appends it to the drill log.
say() {
  printf '%s\n' "$*"
  printf '%s\n' "$*" >> "$LOG_FILE"
}

# sayf prints a formatted line to the terminal and appends it to the log.
sayf() {
  local fmt="$1"
  shift
  printf "$fmt" "$@"
  printf "$fmt" "$@" >> "$LOG_FILE"
}

record() { # record <PASS|FAIL|SKIP> <check-id> <detail>
  RESULTS+=("$1"$'\t'"$2"$'\t'"$3")
  case "$1" in
    PASS) sayf '  [PASS] %-28s %s\n' "$2" "$3" ;;
    FAIL) sayf '  [FAIL] %-28s %s\n' "$2" "$3"; FAILURES=$((FAILURES + 1)) ;;
    *)    sayf '  [%s] %-28s %s\n' "$1" "$2" "$3" ;;
  esac
}

wait_for() { # wait_for <shell-function> <timeout-seconds>
  local fn="$1" timeout="$2" waited=0
  while [ "$waited" -lt "$timeout" ]; do
    if "$fn"; then
      return 0
    fi
    sleep 1
    waited=$((waited + 1))
  done
  return 1
}

redis_ready() {
  docker exec "$REDIS_CONTAINER" redis-cli PING 2>/dev/null | grep -q PONG
}

rabbit_ready() {
  docker exec "$RABBIT_CONTAINER" rabbitmqctl await_startup >/dev/null 2>&1
}

# run_probe <phase> <test-regex> - runs one chaos probe and returns 0 only
# when the probe actually executed AND passed. A probe that did not run must
# never be reported as a green check.
run_probe() {
  local phase="$1" regex="$2" out rc
  out="$(cd "$BACKEND_DIR" && \
    DR_DRILL=1 \
    DR_DRILL_PHASE="$phase" \
    DR_REDIS_ADDR="$REDIS_ADDR" \
    DR_AMQP_URL="$AMQP_URL" \
    DR_DATABASE_URL="$DB_URL" \
    DR_SNAPSHOT_FILE="$SNAPSHOT_FILE" \
    go test -tags chaos -count=1 -v -timeout 180s -run "$regex" ./tests/chaos/... 2>&1)" && rc=0 || rc=$?
  printf '%s\n' "$out" | sed 's/^/      | /' | tee -a "$LOG_FILE"
  if [ "$rc" -ne 0 ]; then
    return 1
  fi
  if ! printf '%s' "$out" | grep -q -- '--- PASS'; then
    say "      ! probe ${regex} did not execute (no \"--- PASS\" line) - refusing a false green"
    return 1
  fi
  return 0
}

build_snapshot_sql() {
  local sql="" table
  for table in $CRITICAL_TABLES; do
    if [ -n "$sql" ]; then
      sql="${sql} UNION ALL "
    fi
    sql="${sql}SELECT '${table}'::text AS t, count(*)::bigint AS c FROM public.${table}"
  done
  printf "SELECT json_build_object('captured_at', now()::text, 'tables', (SELECT json_object_agg(t, c) FROM (%s) q), 'rls', (SELECT json_object_agg(tablename, rowsecurity) FROM pg_tables WHERE schemaname='public'))::text;" "$sql"
}

preflight() {
  say "=== PRE-DRILL ==="
  if ! command -v docker >/dev/null 2>&1; then
    say "docker is not installed - cannot run the DR drill"
    return 127
  fi
  if ! docker info >/dev/null 2>&1; then
    say "docker daemon is not reachable - cannot run the DR drill"
    return 127
  fi
  record PASS "preflight/docker" "docker daemon reachable"

  (cd "$REPO_ROOT" && docker compose up -d postgres redis rabbitmq >/dev/null 2>&1) || true
  if ! wait_for redis_ready 60; then
    record FAIL "preflight/redis" "redis container did not become healthy"
    return 1
  fi
  if ! wait_for rabbit_ready 120; then
    record FAIL "preflight/rabbitmq" "rabbitmq container did not become healthy"
    return 1
  fi
  record PASS "preflight/redis" "redis container healthy"
  record PASS "preflight/rabbitmq" "rabbitmq container healthy"

  # DRP Playbook #3 Step 3: the x-delayed-message plugin is a hard
  # prerequisite, without it re-declaring delayed_exchange fails.
  if docker exec "$RABBIT_CONTAINER" rabbitmq-plugins list -e 2>/dev/null | grep -q 'rabbitmq_delayed_message_exchange'; then
    record PASS "preflight/plugin" "rabbitmq_delayed_message_exchange enabled (Playbook #3 Step 3)"
  else
    record FAIL "preflight/plugin" "rabbitmq_delayed_message_exchange NOT enabled (Playbook #3 Step 3)"
  fi
  PREFLIGHT_OK=1
  return 0
}

drill_playbook2_redis() {
  say ""
  say "=== PLAYBOOK #2: REDIS CLUSTER TOTAL FAILURE (DRP 5.3) ==="
  local marker="dr:dill:${STAMP}" t0 t1

  # Phase 2 Step 3 precondition: write a state marker so the drill can prove
  # the data survived the outage (named volume) instead of silently losing it.
  docker exec "$REDIS_CONTAINER" redis-cli SET "$marker" "alive" >/dev/null 2>&1 || true

  say "  fault injection: docker stop ${REDIS_CONTAINER}"
  docker stop "$REDIS_CONTAINER" >/dev/null 2>&1 || true
  REDIS_DOWN=1

  if run_probe down '^TestDrill_Redis$'; then
    record PASS "PB2/down" "readiness probe fails while redis is down (degraded mode)"
  else
    record FAIL "PB2/down" "readiness probe did not fail while redis was down"
  fi

  say "  recovery: docker start ${REDIS_CONTAINER}"
  t0="$(date +%s)"
  docker start "$REDIS_CONTAINER" >/dev/null 2>&1
  REDIS_DOWN=0
  wait_for redis_ready 60 || true
  t1="$(date +%s)"
  sayf '  redis RTO: %ss (DRP target <= 300s)\n' "$((t1 - t0))"
  RTO_MEASURED=1

  if run_probe up '^TestDrill_Redis$'; then
    record PASS "PB2/recovery" "readiness probe healthy again (RTO $((t1 - t0))s)"
  else
    record FAIL "PB2/recovery" "readiness probe still failing after recovery"
  fi

  if [ "$(docker exec "$REDIS_CONTAINER" redis-cli GET "$marker" 2>/dev/null | tr -d '\r')" = "alive" ]; then
    record PASS "PB2/state" "redis state survived the outage (RPO: state is rebuildable)"
  else
    record FAIL "PB2/state" "redis state marker lost across the outage"
  fi
  docker exec "$REDIS_CONTAINER" redis-cli DEL "$marker" >/dev/null 2>&1 || true
}

drill_playbook3_rabbitmq() {
  say ""
  say "=== PLAYBOOK #3: RABBITMQ FAILURE (DRP 5.4) ==="
  local t0 t1

  say "  fault injection: docker stop ${RABBIT_CONTAINER}"
  docker stop "$RABBIT_CONTAINER" >/dev/null 2>&1 || true
  RABBIT_DOWN=1

  if run_probe down '^TestDrill_RabbitMQ$'; then
    record PASS "PB3/down" "broker connection fails while rabbitmq is down (degraded path)"
  else
    record FAIL "PB3/down" "broker connection did not fail while rabbitmq was down"
  fi

  say "  recovery: docker start ${RABBIT_CONTAINER}"
  t0="$(date +%s)"
  docker start "$RABBIT_CONTAINER" >/dev/null 2>&1
  RABBIT_DOWN=0
  wait_for rabbit_ready 120 || true
  t1="$(date +%s)"
  sayf '  rabbitmq RTO: %ss (DRP target <= 600s)\n' "$((t1 - t0))"
  RTO_MEASURED=1

  if run_probe up '^TestDrill_RabbitMQ$'; then
    record PASS "PB3/recovery" "broker connection healthy again (RTO $((t1 - t0))s)"
  else
    record FAIL "PB3/recovery" "broker connection still failing after recovery"
  fi
}

drill_playbook1_postgres() {
  say ""
  say "=== PLAYBOOK #1: POSTGRES PRIMARY FAILURE / CORRUPTION (DRP 5.2) ==="
  local t0 t1

  # Phase 1 - preparation. A dedicated drill database keeps etix_flash_dev
  # untouched (DRP 7.2: dry-run, no impact).
  say "  preparing drill database ${DRILL_DB}"
  docker exec "$PG_CONTAINER" psql -U postgres -c "DROP DATABASE IF EXISTS ${DRILL_DB};" >/dev/null 2>&1 || true
  if ! docker exec "$PG_CONTAINER" psql -U postgres -c "CREATE DATABASE ${DRILL_DB};" >/dev/null 2>&1; then
    record FAIL "PB1/prepare" "could not create drill database ${DRILL_DB}"
    return 1
  fi

  if (cd "$BACKEND_DIR" && go run ./cmd/migrate -path ./migrations -database "$DB_URL" up >/dev/null 2>&1); then
    record PASS "PB1/migrate" "migrations applied to ${DRILL_DB} (Playbook #1 Step 3)"
  else
    record FAIL "PB1/migrate" "migrate up failed for ${DRILL_DB}"
    return 1
  fi

  if docker exec -i "$PG_CONTAINER" psql -U postgres -d "$DRILL_DB" -v ON_ERROR_STOP=1 \
      < "${BACKEND_DIR}/tests/fixtures/seed.sql" >/dev/null 2>&1; then
    record PASS "PB1/seed" "seed fixtures loaded into ${DRILL_DB}"
  else
    record FAIL "PB1/seed" "seeding ${DRILL_DB} failed"
    return 1
  fi

  # Phase 1 Step 4 - capture the backup + the pre-corruption fingerprint.
  if docker exec "$PG_CONTAINER" pg_dump -U postgres -d "$DRILL_DB" -Fc -f "$CONTAINER_DUMP" >/dev/null 2>&1; then
    record PASS "PB1/backup" "backup captured (pg_dump custom format)"
  else
    record FAIL "PB1/backup" "pg_dump failed for ${DRILL_DB}"
    return 1
  fi
  if docker exec "$PG_CONTAINER" psql -U postgres -d "$DRILL_DB" -t -A -c "$(build_snapshot_sql)" > "$SNAPSHOT_FILE" 2>/dev/null; then
    record PASS "PB1/snapshot" "pre-corruption fingerprint written to ${SNAPSHOT_FILE}"
  else
    record FAIL "PB1/snapshot" "could not capture the pre-corruption fingerprint"
    return 1
  fi

  # Phase 2 - simulate the corruption (drop the whole public schema).
  say "  fault injection: DROP SCHEMA public CASCADE in ${DRILL_DB}"
  docker exec "$PG_CONTAINER" psql -U postgres -d "$DRILL_DB" -v ON_ERROR_STOP=1 \
    -c "DROP SCHEMA public CASCADE; DROP SCHEMA IF EXISTS auth CASCADE; CREATE SCHEMA public;" >/dev/null 2>&1 || true

  if run_probe down '^TestDrill_Postgres_CorruptionDetected$'; then
    record PASS "PB1/detect" "corruption detected (critical tables missing)"
  else
    record FAIL "PB1/detect" "corruption was not detected"
  fi

  # Phase 2 Step 2 - restore from the backup.
  say "  recovery: pg_restore --clean --if-exists"
  t0="$(date +%s)"
  if docker exec "$PG_CONTAINER" pg_restore --clean --if-exists -U postgres -d "$DRILL_DB" "$CONTAINER_DUMP" \
      > "${LOG_DIR}/pg_restore_${STAMP}.log" 2>&1; then
    t1="$(date +%s)"
    RTO_MEASURED=1
    record PASS "PB1/restore" "pg_restore completed in $((t1 - t0))s (DRP target RTO <= 900s)"
  else
    t1="$(date +%s)"
    record FAIL "PB1/restore" "pg_restore failed after $((t1 - t0))s - see pg_restore_${STAMP}.log"
  fi

  if run_probe up '^TestDrill_Postgres_RestoreIntegrity$'; then
    record PASS "PB1/verify" "tables + row counts + RLS flags match the pre-corruption snapshot"
  else
    record FAIL "PB1/verify" "restored database does not match the pre-corruption snapshot"
  fi
}

# status_for <check-id>... -> "x" when every matching check passed, " " when a
# matching check failed and "-" when no check matched (not applicable to the
# selected playbook). The DRP 7.3 checklist must reflect what actually ran.
status_for() {
  local needle entry id state matched=0 ok=1
  for needle in "$@"; do
    for entry in "${RESULTS[@]}"; do
      state="${entry%%$'\t'*}"
      id="${entry#*$'\t'}"
      id="${id%%$'\t'*}"
      if [ "$id" = "$needle" ]; then
        matched=1
        [ "$state" = "PASS" ] || ok=0
      fi
    done
  done
  if [ "$matched" = "0" ]; then
    printf -- '-'
  elif [ "$ok" = "1" ]; then
    printf -- 'x'
  else
    printf -- ' '
  fi
}

any_check() { # any_check <id-prefix>... -> "x" when at least one check ran
  local needle entry id
  for needle in "$@"; do
    for entry in "${RESULTS[@]}"; do
      id="${entry#*$'\t'}"
      id="${id%%$'\t'*}"
      case "$id" in
        "$needle"*) printf -- 'x'; return 0 ;;
      esac
    done
  done
  printf -- '-'
}

summary() {
  say ""
  say "=== DR DRILL SUMMARY (DRP 7.3 checklist) ==="
  local entry
  for entry in "${RESULTS[@]}"; do
    say "  ${entry}"
  done
  say ""
  sayf '  checks: %d   failures: %d\n' "${#RESULTS[@]}" "$FAILURES"
  say ""
  local env_status backup_status timers_status steps_status recovery_status rto_status
  if [ "$PREFLIGHT_OK" = "1" ]; then env_status="x"; else env_status=" "; fi
  backup_status="$(status_for "PB1/backup")"
  if [ "$RTO_MEASURED" = "1" ]; then timers_status="x"; rto_status="x"; else timers_status="-"; rto_status="-"; fi
  steps_status="$(any_check "PB1/" "PB2/" "PB3/")"
  recovery_status="$(status_for "PB2/recovery" "PB3/recovery" "PB1/verify")"
  say "  Pre-Drill   : [$env_status] safe environment prepared   [$backup_status] backup captured   [x] success criteria set"
  say "  During-Drill: [$timers_status] timers started              [$steps_status] playbook steps executed"
  say "  Post-Drill  : [$recovery_status] recovery verified           [$rto_status] RTO measured      [x] findings written"
  say ""
  if [ "$FAILURES" -eq 0 ]; then
    say "  RESULT: PASS - all DR drill checks green"
  else
    sayf '  RESULT: FAIL - %d DR drill check(s) failed\n' "$FAILURES"
  fi
}

cleanup() {
  local rc=$?
  if [ "$CLEANED" = "1" ]; then
    return $rc
  fi
  CLEANED=1
  say ""
  say "=== CLEANUP ==="
  if [ "$REDIS_DOWN" = "1" ]; then
    docker start "$REDIS_CONTAINER" >/dev/null 2>&1 || true
    REDIS_DOWN=0
    say "  redis container restarted"
  fi
  if [ "$RABBIT_DOWN" = "1" ]; then
    docker start "$RABBIT_CONTAINER" >/dev/null 2>&1 || true
    RABBIT_DOWN=0
    say "  rabbitmq container restarted"
  fi
  if [ "$DRILL_DB_EPHEMERAL" = "1" ] && [ "$KEEP_DB" = "0" ]; then
    docker exec "$PG_CONTAINER" psql -U postgres -c "DROP DATABASE IF EXISTS ${DRILL_DB};" >/dev/null 2>&1 || true
    say "  drill database ${DRILL_DB} dropped"
  fi
  return $rc
}

main() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --playbook)
        PLAYBOOK="${2:-}"
        shift 2
        ;;
      --keep-db)
        KEEP_DB=1
        shift
        ;;
      -h|--help)
        usage
        return 0
        ;;
      *)
        say "unknown argument: $1"
        say ""
        usage
        return 2
        ;;
    esac
  done

  mkdir -p "$LOG_DIR"

  say "=== DR DRILLS STARTING ==="
  date '+%Y-%m-%d %H:%M:%S %Z' | tee -a "$LOG_FILE"
  sayf '  playbook(s): %s   drill db: %s   redis: %s\n' "$PLAYBOOK" "$DRILL_DB" "$REDIS_ADDR"

  if ! preflight; then
    record FAIL "preflight" "pre-flight checks failed"
    summary
    return 1
  fi

  # The playbook helpers report their own failures through record FAIL and
  # return non-zero. They are called with "|| true" so that `set -e` does not
  # abort the run before summary() prints the DRP 7.3 checklist: an aborted
  # drill without a summary is exactly the report a DR review cannot use.
  case "$PLAYBOOK" in
    all)
      drill_playbook2_redis || true
      drill_playbook3_rabbitmq || true
      drill_playbook1_postgres || true
      ;;
    1)
      drill_playbook1_postgres || true
      ;;
    2)
      drill_playbook2_redis || true
      ;;
    3)
      drill_playbook3_rabbitmq || true
      ;;
    *)
      say "unknown playbook: ${PLAYBOOK} (expected 1, 2, 3 or all)"
      return 2
      ;;
  esac

  summary
  say ""
  say "=== DR DRILLS COMPLETE ==="
  date '+%Y-%m-%d %H:%M:%S %Z' | tee -a "$LOG_FILE"

  if [ "$FAILURES" -ne 0 ]; then
    return 1
  fi
  return 0
}

# INT / TERM must unwind through the cleanup trap so an aborted drill never
# leaves a container stopped. The EXIT trap does the actual work.
trap 'exit 130' INT
trap 'exit 143' TERM
trap cleanup EXIT

main "$@"
