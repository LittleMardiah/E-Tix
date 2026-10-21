//go:build integration

package admin_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/admin"
)

func setupAdminRepo(t *testing.T) (*pgxpool.Pool, admin.AdminRepository) {
	t.Helper()
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		t.Skip("TEST_DB_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(ctx))
	return pool, admin.NewPostgresAdminRepository(pool)
}

// testPhone builds a DB-valid phone number from a UUID.
// users.phone CHECK (phone_valid) enforces '^\+?[0-9]{10,15}$', so raw UUID hex
// characters (a-f) are mapped to digits before use.
func testPhone(id uuid.UUID) string {
	s := id.String()
	digits := make([]byte, 0, 8)
	for i := 0; i < 8; i++ {
		c := s[i]
		n := int(c - '0')
		if c < '0' || c > '9' {
			n = 10 + int(c-'a')
		}
		digits = append(digits, byte('0'+n%10))
	}
	return "+6280" + string(digits)
}

// TestRepo_GetUserByID_Success uses seeded user (eo@test.local).
func TestRepo_GetUserByID_Success(t *testing.T) {
	pool, repo := setupAdminRepo(t)
	ctx := context.Background()

	var userID uuid.UUID
	err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email='eo@test.local' LIMIT 1`).Scan(&userID)
	require.NoError(t, err, "seed user eo@test.local not found")

	u, err := repo.GetUserByID(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, u)
	assert.Equal(t, userID, u.UserID)
	assert.Equal(t, "eo@test.local", u.Email)
	assert.NotEmpty(t, u.Role)
	assert.NotEmpty(t, u.LoyaltyTier) // computed field
}

func TestRepo_GetUserByID_NotFound(t *testing.T) {
	_, repo := setupAdminRepo(t)
	ctx := context.Background()

	_, err := repo.GetUserByID(ctx, uuid.New())
	assert.ErrorIs(t, err, admin.ErrAdminNotFound)
}

func TestRepo_ListUsers_Paged(t *testing.T) {
	_, repo := setupAdminRepo(t)
	ctx := context.Background()

	users, total, err := repo.ListUsers(ctx, "", "", "", 1, 5)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(users), 5)
	assert.GreaterOrEqual(t, total, 0)
	if len(users) > 0 {
		assert.NotEmpty(t, users[0].UserID)
		assert.NotEmpty(t, users[0].LoyaltyTier)
	}
}

func TestRepo_UpdateUserStatus_Success(t *testing.T) {
	pool, repo := setupAdminRepo(t)
	ctx := context.Background()

	// Insert test user
	testID := uuid.New()
	email := "p9a1-" + testID.String() + "@e-tix.internal"
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, phone, name, password_hash, role, status, base_score)
		VALUES ($1, $2, $3, 'P9a1 Test', 'hash', 'customer', 'ACTIVE', 50)`,
		testID, email, testPhone(testID))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, testID)
	})

	// Update via BEGIN -> Update -> COMMIT
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	require.NoError(t, repo.UpdateUserStatus(ctx, tx, testID, "SUSPENDED"))
	require.NoError(t, tx.Commit(ctx))

	// Verify
	var status string
	err = pool.QueryRow(ctx, `SELECT status::text FROM users WHERE id=$1`, testID).Scan(&status)
	require.NoError(t, err)
	assert.Equal(t, "SUSPENDED", status)
}

func TestRepo_UpdateUserStatus_NotFound(t *testing.T) {
	pool, repo := setupAdminRepo(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	err = repo.UpdateUserStatus(ctx, tx, uuid.New(), "SUSPENDED")
	assert.ErrorIs(t, err, admin.ErrAdminNotFound)
}

func TestRepo_InsertBlacklist_Success(t *testing.T) {
	pool, repo := setupAdminRepo(t)
	ctx := context.Background()

	// Unique type/value to avoid collisions
	value := "p9a1-" + uuid.NewString() + ".test"
	reason := "test insert"
	exp := time.Now().Add(1 * time.Hour)
	entry := &admin.BlacklistEntry{
		Type:      "ip",
		Value:     value,
		Reason:    &reason,
		ExpiresAt: &exp,
	}
	err := repo.InsertBlacklist(ctx, entry)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, entry.ID)
	assert.False(t, entry.AddedAt.IsZero())

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM blacklist WHERE id=$1`, entry.ID)
	})
}

func TestRepo_InsertBlacklist_Duplicate(t *testing.T) {
	pool, repo := setupAdminRepo(t)
	ctx := context.Background()

	value := "p9a1-dup-" + uuid.NewString() + ".test"
	reason := "test duplicate"
	first := &admin.BlacklistEntry{Type: "ip", Value: value, Reason: &reason}
	require.NoError(t, repo.InsertBlacklist(ctx, first))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM blacklist WHERE value=$1`, value)
	})

	second := &admin.BlacklistEntry{Type: "ip", Value: value, Reason: &reason}
	err := repo.InsertBlacklist(ctx, second)
	assert.ErrorIs(t, err, admin.ErrAdminConflict)
}

func TestRepo_DeleteBlacklist_NotFound(t *testing.T) {
	_, repo := setupAdminRepo(t)
	ctx := context.Background()

	_, err := repo.DeleteBlacklist(ctx, uuid.New())
	assert.ErrorIs(t, err, admin.ErrAdminNotFound)
}
