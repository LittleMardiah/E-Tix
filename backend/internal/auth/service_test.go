package auth

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

func newTestRedisClient(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)

	client := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = client.Close() })

	return client, mr
}

func newTestAuthService(t *testing.T) (*AuthService, *miniredis.Miniredis) {
	t.Helper()
	client, mr := newTestRedisClient(t)
	svc := NewAuthService(nil, nil, client, nil, NewTokenBlacklistService(client))
	return svc, mr
}

func TestComputeLoyaltyTier_Boundaries(t *testing.T) {
	cases := []struct {
		name      string
		base      int
		purchases int
		expTier   string
		expScore  int
	}{
		{"base 90 -> TIER_1", 90, 0, "TIER_1", 90},
		{"base 89 -> TIER_2", 89, 0, "TIER_2", 89},
		{"base 70 -> TIER_2", 70, 0, "TIER_2", 70},
		{"base 69 -> TIER_3", 69, 0, "TIER_3", 69},
		{"base 50 + 3 purchases -> 80 TIER_2", 50, 3, "TIER_2", 80},
		{"base 50 + 5 purchases -> 100 TIER_1", 50, 5, "TIER_1", 100},
		{"base 100 -> TIER_1", 100, 0, "TIER_1", 100},
		{"base 0 -> TIER_3", 0, 0, "TIER_3", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tier, score := ComputeLoyaltyTier(tc.base, tc.purchases)
			assert.Equal(t, tc.expTier, tier)
			assert.Equal(t, tc.expScore, score)
		})
	}
}

func TestValidatePassword_TooShort(t *testing.T) {
	err := validatePassword("Ab1!")
	require.Error(t, err)

	var vErr *ValidationError
	require.ErrorAs(t, err, &vErr)
	assert.Equal(t, "password", vErr.Field)
}

func TestValidatePassword_NoUpper(t *testing.T) {
	err := validatePassword("abcd1234!")
	require.Error(t, err)

	var vErr *ValidationError
	require.ErrorAs(t, err, &vErr)
	assert.Equal(t, "password", vErr.Field)
}

func TestValidatePassword_Valid(t *testing.T) {
	assert.NoError(t, validatePassword("Abcd1234!"))
}

func TestGenerateOTP_6Digits(t *testing.T) {
	svc, _ := newTestAuthService(t)

	code, err := svc.GenerateOTP(context.Background(), uuid.New(), "otp@test.local")
	require.NoError(t, err)
	assert.Len(t, code, OTPCodeLength)
	assert.True(t, isNumeric(code))
}

func TestGenerateOTP_RedisStored(t *testing.T) {
	svc, mr := newTestAuthService(t)
	uid := uuid.New()

	code, err := svc.GenerateOTP(context.Background(), uid, "otp@test.local")
	require.NoError(t, err)

	key := otpKey(uid)
	require.True(t, mr.Exists(key))

	stored, err := mr.Get(key)
	require.NoError(t, err)
	assert.Equal(t, code, stored)
	assert.Equal(t, OTPTTL, mr.TTL(key))
}

func TestVerifyOTP_Success(t *testing.T) {
	svc, mr := newTestAuthService(t)
	uid := uuid.New()
	const code = "123456"
	mr.Set(otpKey(uid), code)

	require.NoError(t, svc.VerifyOTP(context.Background(), uid, code))
	assert.False(t, mr.Exists(otpKey(uid)))
}

func TestVerifyOTP_Expired(t *testing.T) {
	svc, _ := newTestAuthService(t)
	uid := uuid.New()

	err := svc.VerifyOTP(context.Background(), uid, "123456")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOTPExpired)
}

func TestVerifyOTP_InvalidCode(t *testing.T) {
	svc, mr := newTestAuthService(t)
	uid := uuid.New()
	mr.Set(otpKey(uid), "123456")

	err := svc.VerifyOTP(context.Background(), uid, "654321")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOTPInvalid)
}
