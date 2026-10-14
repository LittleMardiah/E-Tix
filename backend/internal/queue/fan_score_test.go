package queue

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/auth"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

type mockUserRepo struct {
	user  *auth.User
	err   error
	calls int
}

func (m *mockUserRepo) CreateUser(_ context.Context, _ *auth.User) error {
	return errors.New("mockUserRepo: CreateUser not implemented")
}

func (m *mockUserRepo) GetUserByEmail(_ context.Context, _ string) (*auth.User, error) {
	return nil, errors.New("mockUserRepo: GetUserByEmail not implemented")
}

func (m *mockUserRepo) GetUserByID(_ context.Context, _ uuid.UUID) (*auth.User, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.user, nil
}

func newTestFanScoreService(t *testing.T) (*FanScoreService, *redis.Client, *miniredis.Miniredis, *mockUserRepo) {
	t.Helper()

	mr := miniredis.RunT(t)
	client := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = client.Close() })

	repo := &mockUserRepo{user: &auth.User{ID: uuid.New(), Email: "fan@test.local", BaseScore: 65}}
	return NewFanScoreService(client, repo), client, mr, repo
}

func TestFanScore_GetBaseScore_CacheHit(t *testing.T) {
	svc, client, _, repo := newTestFanScoreService(t)
	ctx := context.Background()
	userID := uuid.New()

	require.NoError(t, client.Set(ctx, baseScoreKey(userID), "77", time.Hour).Err())

	score, err := svc.GetBaseScore(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 77, score)
	assert.Equal(t, 0, repo.calls, "cache hit must not call user repo")
}

func TestFanScore_GetBaseScore_CacheMiss(t *testing.T) {
	svc, client, _, repo := newTestFanScoreService(t)
	ctx := context.Background()
	userID := uuid.New()

	score, err := svc.GetBaseScore(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 65, score)
	assert.Equal(t, 1, repo.calls)

	score, err = svc.GetBaseScore(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 65, score)
	assert.Equal(t, 1, repo.calls, "second call must be served from cache")
	require.Equal(t, int64(1), client.Exists(ctx, baseScoreKey(userID)).Val())

	repo.user.BaseScore = 0
	require.NoError(t, client.Del(ctx, baseScoreKey(userID)).Err())

	score, err = svc.GetBaseScore(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, defaultBaseScore, score)
}

func TestFanScore_GetDynamicScore_Default(t *testing.T) {
	svc, client, _, _ := newTestFanScoreService(t)
	ctx := context.Background()
	userID := uuid.New()

	score, err := svc.GetDynamicScore(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 0, score)

	require.NoError(t, client.Set(ctx, dynamicScoreKey(userID), "not-a-number", time.Hour).Err())
	score, err = svc.GetDynamicScore(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 0, score)
}

func TestFanScore_GetDynamicScore_Set(t *testing.T) {
	svc, client, _, _ := newTestFanScoreService(t)
	ctx := context.Background()
	userID := uuid.New()

	require.NoError(t, client.Set(ctx, dynamicScoreKey(userID), "30", time.Hour).Err())
	score, err := svc.GetDynamicScore(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 30, score)

	require.NoError(t, client.Set(ctx, dynamicScoreKey(userID), "-30", time.Hour).Err())
	score, err = svc.GetDynamicScore(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, -30, score)
}

func TestFanScore_ComputeFinalScore_TierBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		base     int
		dyn      int
		expScore int
		expTier  string
	}{
		{"final 91 -> TIER_1 (upper boundary >90)", 91, 0, 91, TierVIP},
		{"final 90 -> TIER_2 (90 is not >90)", 90, 0, 90, TierRegular},
		{"final 89 -> TIER_2", 89, 0, 89, TierRegular},
		{"final 70 -> TIER_2 (lower boundary >=70)", 70, 0, 70, TierRegular},
		{"final 69 -> TIER_3", 69, 0, 69, TierLow},
		{"base 60 + dyn 30 = 90 -> TIER_2", 60, 30, 90, TierRegular},
		{"base 40 + dyn -30 = 10 -> TIER_3", 40, -30, 10, TierLow},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, client, _, repo := newTestFanScoreService(t)
			ctx := context.Background()
			userID := uuid.New()

			repo.user.ID = userID
			repo.user.BaseScore = tc.base
			require.NoError(t, client.Set(ctx, dynamicScoreKey(userID), strconv.Itoa(tc.dyn), time.Hour).Err())

			score, tier, err := svc.ComputeFinalScore(ctx, userID)
			require.NoError(t, err)
			assert.Equal(t, tc.expScore, score)
			assert.Equal(t, tc.expTier, tier)
		})
	}
}

func TestFanScore_TierLabel(t *testing.T) {
	svc, _, _, _ := newTestFanScoreService(t)

	assert.Equal(t, "VIP Fan", svc.TierLabel(TierVIP))
	assert.Equal(t, "Regular Fan", svc.TierLabel(TierRegular))
	assert.Equal(t, "Low Priority", svc.TierLabel(TierLow))
	assert.Equal(t, "Unknown", svc.TierLabel("TIER_99"))
}
