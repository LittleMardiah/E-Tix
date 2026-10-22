package booking

// FIX P9a-9c — TD-F3-03: benchmark of the GET /seats bitmap read path.
//
// Finding (verified in code, see docs/TECHNICAL DEBT.txt TD-F3-03): the
// shipped read path is ALREADY the optimized form described by the TD's own
// "Rencana Fix". BookingService.readLockedBitmap
// (internal/booking/service.go:318) fetches the whole bitmap with a SINGLE
// Redis GET and decodes the bits in Go (bitAt) — there is no per-bit GETBIT
// loop anywhere in the codebase (the only SetBit left is the write path in
// internal/admin/service.go:204).
//
// The TD trigger is "profil latency p95 > 100ms di staging/prod". This file
// measures that latency locally so the decision is backed by numbers instead
// of assumptions, and compares it against the per-bit variant the TD feared.

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	redisclient "github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// p9a9cBenchSeats mirrors the worst case named in TD-F3-03 (a 5000 seat
// section).
const p9a9cBenchSeats = 5000

// p9a9cBenchEnv holds the benchmark fixture.
type p9a9cBenchEnv struct {
	mr        *miniredis.Miniredis
	rdb       *redisclient.Client
	svc       *BookingService
	eventID   uuid.UUID
	sectionID uuid.UUID
	key       string
}

// p9a9cBenchSetup builds a section bitmap with one seat locked out of three.
func p9a9cBenchSetup(tb testing.TB) *p9a9cBenchEnv {
	tb.Helper()
	mr := miniredis.RunT(tb)
	raw := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	tb.Cleanup(func() { _ = raw.Close() })
	rdb := &redisclient.Client{Client: raw}

	eventID := uuid.New()
	sectionID := uuid.New()
	key := fmt.Sprintf("seats:%s:%s:v%d", eventID, sectionID, 1)

	ctx := context.Background()
	for pos := 1; pos <= p9a9cBenchSeats; pos += 3 {
		if err := rdb.SetBit(ctx, key, int64(pos-1), 1).Err(); err != nil {
			tb.Fatalf("setbit: %v", err)
		}
	}

	return &p9a9cBenchEnv{
		mr:        mr,
		rdb:       rdb,
		svc:       &BookingService{rdb: rdb},
		eventID:   eventID,
		sectionID: sectionID,
		key:       key,
	}
}

// BenchmarkReadLockedBitmap_ProductionPath measures the shipped read path:
// one GET for the whole bitmap + Go-side bit decoding.
func BenchmarkReadLockedBitmap_ProductionPath(b *testing.B) {
	env := p9a9cBenchSetup(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := env.svc.readLockedBitmap(ctx, env.eventID, env.sectionID, 1, p9a9cBenchSeats); err != nil {
			b.Fatalf("readLockedBitmap: %v", err)
		}
	}
}

// BenchmarkReadLockedBitmap_PerBitGetBit measures the variant TD-F3-03
// described ("granularity per-bit via loop GETBIT (position-by-position)").
// It is kept as the reference point that proves the shipped path is the fast
// one — NOT as a candidate implementation.
func BenchmarkReadLockedBitmap_PerBitGetBit(b *testing.B) {
	env := p9a9cBenchSetup(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := make(map[int]bool, p9a9cBenchSeats)
		for pos := 1; pos <= p9a9cBenchSeats; pos++ {
			if v, err := env.rdb.GetBit(ctx, env.key, int64(pos-1)).Result(); err != nil {
				b.Fatalf("getbit: %v", err)
			} else if v == 1 {
				out[pos] = true
			}
		}
	}
}

// TestP9a9cReadLockedBitmap_MatchesPerBitReference proves that the shipped
// single-GET decode returns exactly the same lock set as a per-bit GETBIT
// walk, so no behavioural regression was traded for the speed-up.
func TestP9a9cReadLockedBitmap_MatchesPerBitReference(t *testing.T) {
	env := p9a9cBenchSetup(t)
	ctx := context.Background()

	locked, err := env.svc.readLockedBitmap(ctx, env.eventID, env.sectionID, 1, p9a9cBenchSeats)
	if err != nil {
		t.Fatalf("readLockedBitmap: %v", err)
	}

	reference := make(map[int]bool)
	for pos := 1; pos <= p9a9cBenchSeats; pos++ {
		v, err := env.rdb.GetBit(ctx, env.key, int64(pos-1)).Result()
		if err != nil {
			t.Fatalf("getbit: %v", err)
		}
		if v == 1 {
			reference[pos] = true
		}
	}

	if len(locked) != len(reference) {
		t.Fatalf("locked set size mismatch: production=%d per-bit=%d", len(locked), len(reference))
	}
	for pos := range reference {
		if !locked[pos] {
			t.Fatalf("position %d locked per-bit but not in the production decode", pos)
		}
	}
}
