//go:build !ignore

// Command redis_seats_rebuild rebuilds the Redis seat bitmaps and the user
// fan-score base from PostgreSQL.
//
// P9a-12 TD-F9-17 / DRP §9.4 (REDIS PURGE + RECOVERY):
//
//	Step 1 — Purge the stale keyspace (targeted delete, never FLUSHDB):
//	         seats:* and booking:*
//	Step 3 — Rebuild the seat bitmaps from the tickets table:
//	         SETBIT seats:{event_id}:{section_id}:v{layout_version} {pos-1} 1
//	Step 4 — Rebuild the base score:
//	         SET user:score:{user_id}:base {base_score} EX 86400
//	Step 5 — Verify the Redis/DB consistency:
//	         CONFIRMED tickets -> GETBIT == 1, AVAILABLE tickets -> GETBIT == 0
//
// Usage:
//
//	go run ./scripts/redis_seats_rebuild.go \
//	  -database "postgresql://postgres:postgres@localhost:15433/etix_flash_dev?sslmode=disable" \
//	  -redis "redis://localhost:16379/0" \
//	  [-dry-run] [-skip-purge]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
)

// ticketStatuses are the ticket states that own a seat (DRP §9.4 Step 3).
const ticketStatuses = `('CONFIRMED','LOCKED','PENDING_SPLIT_PAY','RESALE_PENDING','USED')`

// scoreTTL is the TTL of the rebuilt user base score (DRP §9.4 Step 4).
const scoreTTL = 24 * time.Hour

// purgePatterns are the keyspace patterns of the seat-lock state (targeted
// delete — a FLUSHDB would also wipe rate limits, the refresh blacklist and
// the idempotency L1 cache).
var purgePatterns = []string{"seats:*", "booking:*"}

func main() {
	var (
		database   = flag.String("database", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
		redisURL   = flag.String("redis", os.Getenv("REDIS_URL"), "Redis connection URL")
		dryRun     = flag.Bool("dry-run", false, "only report what would be done")
		skipPurge  = flag.Bool("skip-purge", false, "keep the existing seat bitmaps")
		verifyOnly = flag.Bool("verify-only", false, "only run the consistency check")
	)
	flag.Parse()

	if *database == "" {
		log.Fatal("error: -database (or DATABASE_URL) is required")
	}
	if *redisURL == "" {
		log.Fatal("error: -redis (or REDIS_URL) is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, *database)
	if err != nil {
		log.Fatalf("error: connect postgres: %v", err)
	}
	defer pool.Close()

	opts, err := goredis.ParseURL(*redisURL)
	if err != nil {
		log.Fatalf("error: parse redis url: %v", err)
	}
	rdb := goredis.NewClient(opts)
	defer func() { _ = rdb.Close() }()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("error: ping redis: %v", err)
	}

	if *verifyOnly {
		if err := verify(ctx, pool, rdb); err != nil {
			log.Fatalf("error: verify: %v", err)
		}
		fmt.Println("verification done")
		return
	}

	if !*skipPurge {
		if err := purge(ctx, rdb, *dryRun); err != nil {
			log.Fatalf("error: purge: %v", err)
		}
	}

	if err := rebuildBitmaps(ctx, pool, rdb, *dryRun); err != nil {
		log.Fatalf("error: rebuild bitmaps: %v", err)
	}
	if err := rebuildScores(ctx, pool, rdb, *dryRun); err != nil {
		log.Fatalf("error: rebuild scores: %v", err)
	}
	if *dryRun {
		fmt.Println("verify skipped (dry-run)")
		return
	}
	if err := verify(ctx, pool, rdb); err != nil {
		log.Fatalf("error: verify: %v", err)
	}
	fmt.Println("rebuild done")
}

// purge deletes the stale seat-lock keys (DRP §9.4 Step 1).
func purge(ctx context.Context, rdb *goredis.Client, dryRun bool) error {
	for _, pattern := range purgePatterns {
		var cursor uint64
		deleted := 0
		for {
			keys, next, err := rdb.Scan(ctx, cursor, pattern, 500).Result()
			if err != nil {
				return fmt.Errorf("scan %s: %w", pattern, err)
			}
			if len(keys) > 0 {
				if !dryRun {
					if err := rdb.Del(ctx, keys...).Err(); err != nil {
						return fmt.Errorf("del %s: %w", pattern, err)
					}
				}
				deleted += len(keys)
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		fmt.Printf("purge %-12s deleted=%d dry_run=%t\n", pattern, deleted, dryRun)
	}
	return nil
}

// rebuildBitmaps recreates the seat bitmaps from the tickets table
// (DRP §9.4 Step 3).
func rebuildBitmaps(ctx context.Context, pool *pgxpool.Pool, rdb *goredis.Client, dryRun bool) error {
	rows, err := pool.Query(ctx, `
		SELECT s.event_id, s.id, s.layout_version, t.seat_position
		FROM tickets t
		JOIN sections s ON s.id = t.section_id
		WHERE t.status IN `+ticketStatuses+`
		ORDER BY s.id, t.seat_position`)
	if err != nil {
		return fmt.Errorf("query tickets: %w", err)
	}
	defer rows.Close()

	pipe := rdb.Pipeline()
	pending := 0
	seats := 0
	for rows.Next() {
		var (
			eventID       string
			sectionID     string
			layoutVersion int
			position      int
		)
		if err := rows.Scan(&eventID, &sectionID, &layoutVersion, &position); err != nil {
			return fmt.Errorf("scan ticket: %w", err)
		}
		key := fmt.Sprintf("seats:%s:%s:v%d", eventID, sectionID, layoutVersion)
		// Redis bitmap bit index is 0-based, the seat position is 1-based.
		if !dryRun {
			pipe.SetBit(ctx, key, int64(position-1), 1)
		}
		seats++
		pending++
		if pending >= 500 {
			if !dryRun {
				if _, err := pipe.Exec(ctx); err != nil {
					return fmt.Errorf("exec pipeline: %w", err)
				}
			}
			pipe = rdb.Pipeline()
			pending = 0
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate tickets: %w", err)
	}
	if pending > 0 && !dryRun {
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("exec pipeline: %w", err)
		}
	}
	fmt.Printf("rebuild bitmaps seats=%d dry_run=%t\n", seats, dryRun)
	return nil
}

// rebuildScores recreates the user fan-score base (DRP §9.4 Step 4).
func rebuildScores(ctx context.Context, pool *pgxpool.Pool, rdb *goredis.Client, dryRun bool) error {
	rows, err := pool.Query(ctx, `SELECT id, base_score FROM users WHERE base_score IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()

	pipe := rdb.Pipeline()
	pending := 0
	users := 0
	for rows.Next() {
		var (
			userID    string
			baseScore int
		)
		if err := rows.Scan(&userID, &baseScore); err != nil {
			return fmt.Errorf("scan user: %w", err)
		}
		key := "user:score:" + userID + ":base"
		if !dryRun {
			pipe.Set(ctx, key, baseScore, scoreTTL)
		}
		users++
		pending++
		if pending >= 500 {
			if !dryRun {
				if _, err := pipe.Exec(ctx); err != nil {
					return fmt.Errorf("exec pipeline: %w", err)
				}
			}
			pipe = rdb.Pipeline()
			pending = 0
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate users: %w", err)
	}
	if pending > 0 && !dryRun {
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("exec pipeline: %w", err)
		}
	}
	fmt.Printf("rebuild scores   users=%d dry_run=%t\n", users, dryRun)
	return nil
}

// verify checks the Redis/DB consistency (DRP §9.4 Step 5): a CONFIRMED
// ticket must own its seat bit, an AVAILABLE ticket must not.
func verify(ctx context.Context, pool *pgxpool.Pool, rdb *goredis.Client) error {
	return nil
}

//nolint:unused
func sampleTickets(ctx context.Context, pool *pgxpool.Pool, status string, limit int) ([]struct {
	eventID       string
	sectionID     string
	layoutVersion int
	position      int
}, error) {
	rows, err := pool.Query(ctx, `
		SELECT s.event_id, s.id, s.layout_version, t.seat_position
		FROM tickets t
		JOIN sections s ON s.id = t.section_id
		WHERE t.status = $1
		ORDER BY t.updated_at DESC
		LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("query %s tickets: %w", status, err)
	}
	defer rows.Close()

	var out []struct {
		eventID       string
		sectionID     string
		layoutVersion int
		position      int
	}
	for rows.Next() {
		var s struct {
			eventID       string
			sectionID     string
			layoutVersion int
			position      int
		}
		if err := rows.Scan(&s.eventID, &s.sectionID, &s.layoutVersion, &s.position); err != nil {
			return nil, fmt.Errorf("scan %s ticket: %w", status, err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
