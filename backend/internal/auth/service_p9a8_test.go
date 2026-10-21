package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

type p9a8UserRepo struct {
	createErr error
	created   *User

	emailUser *User
	emailErr  error

	idUser *User
	idErr  error
}

func (r *p9a8UserRepo) CreateUser(_ context.Context, user *User) error {
	if r.createErr != nil {
		return r.createErr
	}
	user.ID = uuid.New()
	r.created = user
	return nil
}

func (r *p9a8UserRepo) GetUserByEmail(_ context.Context, _ string) (*User, error) {
	return r.emailUser, r.emailErr
}

func (r *p9a8UserRepo) GetUserByID(_ context.Context, _ uuid.UUID) (*User, error) {
	return r.idUser, r.idErr
}

func newP9a8AuthService(t *testing.T, repo UserRepository) (*AuthService, *JWTService) {
	t.Helper()
	client, _ := newTestRedisClient(t)
	jwtSvc := newTestJWTService(t)
	return NewAuthService(repo, jwtSvc, client, nil, NewTokenBlacklistService(client)), jwtSvc
}

func p9a8User(t *testing.T, password, status string) *User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)
	return &User{
		ID:             uuid.New(),
		Email:          "alice@example.com",
		Phone:          "+6281234567890",
		Name:           "Alice",
		PasswordHash:   string(hash),
		Role:           "customer",
		Status:         status,
		BaseScore:      50,
		TotalPurchases: 3,
	}
}

func p9a8RegisterReq() *RegisterRequest {
	return &RegisterRequest{
		Email:    "new@example.com",
		Phone:    "+6281234567890",
		Name:     "New User",
		Password: "Abcd1234!",
		Role:     "customer",
	}
}

func TestValidationErrorP9a8(t *testing.T) {
	assert.Equal(t, "email: bad email", (&ValidationError{Field: "email", Message: "bad email"}).Error())
	assert.Equal(t, "body is required", (&ValidationError{Message: "body is required"}).Error())
}

func TestAuthServiceRegisterP9a8(t *testing.T) {
	t.Run("nil request", func(t *testing.T) {
		svc, _ := newP9a8AuthService(t, &p9a8UserRepo{})
		_, err := svc.Register(context.Background(), nil)
		var vErr *ValidationError
		require.ErrorAs(t, err, &vErr)
		assert.Equal(t, "body", vErr.Field)
	})

	t.Run("validation failure", func(t *testing.T) {
		svc, _ := newP9a8AuthService(t, &p9a8UserRepo{})
		req := p9a8RegisterReq()
		req.Email = "bad"
		_, err := svc.Register(context.Background(), req)
		var vErr *ValidationError
		require.ErrorAs(t, err, &vErr)
		assert.Equal(t, "email", vErr.Field)
	})

	t.Run("create email conflict", func(t *testing.T) {
		repo := &p9a8UserRepo{createErr: ErrEmailExists}
		svc, _ := newP9a8AuthService(t, repo)
		_, err := svc.Register(context.Background(), p9a8RegisterReq())
		assert.ErrorIs(t, err, ErrEmailExists)
	})

	t.Run("create phone conflict", func(t *testing.T) {
		repo := &p9a8UserRepo{createErr: ErrPhoneExists}
		svc, _ := newP9a8AuthService(t, repo)
		_, err := svc.Register(context.Background(), p9a8RegisterReq())
		assert.ErrorIs(t, err, ErrPhoneExists)
	})

	t.Run("create generic error", func(t *testing.T) {
		repo := &p9a8UserRepo{createErr: errors.New("db down")}
		svc, _ := newP9a8AuthService(t, repo)
		_, err := svc.Register(context.Background(), p9a8RegisterReq())
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrEmailExists)
	})

	t.Run("success", func(t *testing.T) {
		repo := &p9a8UserRepo{}
		svc, _ := newP9a8AuthService(t, repo)
		req := p9a8RegisterReq()
		req.Role = ""

		resp, err := svc.Register(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, repo.created)
		assert.Equal(t, defaultUserRole, repo.created.Role)
		assert.Equal(t, defaultUserStatus, repo.created.Status)
		assert.Equal(t, repo.created.ID.String(), resp.UserID)
		assert.NotEmpty(t, resp.AccessToken)
		assert.NotEmpty(t, resp.RefreshToken)
		assert.Equal(t, registerExpiresIn, resp.ExpiresIn)
	})

	t.Run("otp failure is non-fatal", func(t *testing.T) {
		repo := &p9a8UserRepo{}
		jwtSvc := newTestJWTService(t)
		svc := NewAuthService(repo, jwtSvc, nil, nil, nil)

		resp, err := svc.Register(context.Background(), p9a8RegisterReq())
		require.NoError(t, err)
		assert.NotEmpty(t, resp.AccessToken)
	})
}

func TestAuthServiceLoginP9a8(t *testing.T) {
	t.Run("nil request", func(t *testing.T) {
		svc, _ := newP9a8AuthService(t, &p9a8UserRepo{})
		_, err := svc.Login(context.Background(), nil)
		var vErr *ValidationError
		require.ErrorAs(t, err, &vErr)
		assert.Equal(t, "body", vErr.Field)
	})

	t.Run("missing email", func(t *testing.T) {
		svc, _ := newP9a8AuthService(t, &p9a8UserRepo{})
		_, err := svc.Login(context.Background(), &LoginRequest{Password: "x"})
		var vErr *ValidationError
		require.ErrorAs(t, err, &vErr)
		assert.Equal(t, "email", vErr.Field)
	})

	t.Run("missing password", func(t *testing.T) {
		svc, _ := newP9a8AuthService(t, &p9a8UserRepo{})
		_, err := svc.Login(context.Background(), &LoginRequest{Email: "a@b.com"})
		var vErr *ValidationError
		require.ErrorAs(t, err, &vErr)
		assert.Equal(t, "password", vErr.Field)
	})

	t.Run("user not found", func(t *testing.T) {
		repo := &p9a8UserRepo{emailErr: ErrUserNotFound}
		svc, _ := newP9a8AuthService(t, repo)
		_, err := svc.Login(context.Background(), &LoginRequest{Email: "a@b.com", Password: "x"})
		assert.ErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("pgx no rows", func(t *testing.T) {
		repo := &p9a8UserRepo{emailErr: pgx.ErrNoRows}
		svc, _ := newP9a8AuthService(t, repo)
		_, err := svc.Login(context.Background(), &LoginRequest{Email: "a@b.com", Password: "x"})
		assert.ErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("repo generic error", func(t *testing.T) {
		repo := &p9a8UserRepo{emailErr: errors.New("db down")}
		svc, _ := newP9a8AuthService(t, repo)
		_, err := svc.Login(context.Background(), &LoginRequest{Email: "a@b.com", Password: "x"})
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("wrong password", func(t *testing.T) {
		repo := &p9a8UserRepo{emailUser: p9a8User(t, "Correct1!", "ACTIVE")}
		svc, _ := newP9a8AuthService(t, repo)
		_, err := svc.Login(context.Background(), &LoginRequest{Email: "alice@example.com", Password: "Wrong1!"})
		assert.ErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("inactive account", func(t *testing.T) {
		repo := &p9a8UserRepo{emailUser: p9a8User(t, "Correct1!", "SUSPENDED")}
		svc, _ := newP9a8AuthService(t, repo)
		_, err := svc.Login(context.Background(), &LoginRequest{Email: "alice@example.com", Password: "Correct1!"})
		assert.ErrorIs(t, err, ErrForbidden)
	})

	t.Run("success", func(t *testing.T) {
		user := p9a8User(t, "Correct1!", "ACTIVE")
		repo := &p9a8UserRepo{emailUser: user}
		svc, _ := newP9a8AuthService(t, repo)

		resp, err := svc.Login(context.Background(), &LoginRequest{Email: "alice@example.com", Password: "Correct1!"})
		require.NoError(t, err)
		assert.Equal(t, user.ID.String(), resp.UserID)
		assert.Equal(t, user.Email, resp.Email)
		assert.Equal(t, user.Role, resp.Role)
		assert.Equal(t, "TIER_2", resp.Tier)
		assert.NotEmpty(t, resp.AccessToken)
		assert.NotEmpty(t, resp.RefreshToken)
		assert.Equal(t, loginExpiresIn, resp.ExpiresIn)
	})
}

func TestAuthServiceRefreshTokenP9a8(t *testing.T) {
	t.Run("invalid token", func(t *testing.T) {
		svc, _ := newP9a8AuthService(t, &p9a8UserRepo{})
		_, err := svc.RefreshToken(context.Background(), "not-a-token")
		assert.ErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("access token rejected", func(t *testing.T) {
		svc, jwtSvc := newP9a8AuthService(t, &p9a8UserRepo{})
		access, err := jwtSvc.GenerateAccessToken(p9a8User(t, "Correct1!", "ACTIVE"), "TIER_3")
		require.NoError(t, err)

		_, err = svc.RefreshToken(context.Background(), access)
		assert.ErrorIs(t, err, ErrTokenTypeMismatch)
	})

	t.Run("success and replay rejection", func(t *testing.T) {
		user := p9a8User(t, "Correct1!", "ACTIVE")
		repo := &p9a8UserRepo{idUser: user}
		svc, jwtSvc := newP9a8AuthService(t, repo)

		refresh, err := jwtSvc.GenerateRefreshToken(user)
		require.NoError(t, err)

		resp, err := svc.RefreshToken(context.Background(), refresh)
		require.NoError(t, err)
		assert.NotEmpty(t, resp.AccessToken)
		assert.NotEmpty(t, resp.RefreshToken)
		assert.Equal(t, loginExpiresIn, resp.ExpiresIn)

		_, err = svc.RefreshToken(context.Background(), refresh)
		assert.ErrorIs(t, err, ErrTokenBlacklisted)
	})

	t.Run("user not found", func(t *testing.T) {
		repo := &p9a8UserRepo{idErr: ErrUserNotFound}
		svc, jwtSvc := newP9a8AuthService(t, repo)
		refresh, err := jwtSvc.GenerateRefreshToken(p9a8User(t, "Correct1!", "ACTIVE"))
		require.NoError(t, err)

		_, err = svc.RefreshToken(context.Background(), refresh)
		assert.ErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("repo generic error", func(t *testing.T) {
		repo := &p9a8UserRepo{idErr: errors.New("db down")}
		svc, jwtSvc := newP9a8AuthService(t, repo)
		refresh, err := jwtSvc.GenerateRefreshToken(p9a8User(t, "Correct1!", "ACTIVE"))
		require.NoError(t, err)

		_, err = svc.RefreshToken(context.Background(), refresh)
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("inactive account", func(t *testing.T) {
		user := p9a8User(t, "Correct1!", "SUSPENDED")
		repo := &p9a8UserRepo{idUser: user}
		svc, jwtSvc := newP9a8AuthService(t, repo)
		refresh, err := jwtSvc.GenerateRefreshToken(user)
		require.NoError(t, err)

		_, err = svc.RefreshToken(context.Background(), refresh)
		assert.ErrorIs(t, err, ErrForbidden)
	})
}

func TestAuthServiceLogoutP9a8(t *testing.T) {
	t.Run("empty tokens", func(t *testing.T) {
		svc, _ := newP9a8AuthService(t, &p9a8UserRepo{})
		assert.NoError(t, svc.Logout(context.Background(), "", ""))
	})

	t.Run("invalid tokens ignored", func(t *testing.T) {
		svc, _ := newP9a8AuthService(t, &p9a8UserRepo{})
		assert.NoError(t, svc.Logout(context.Background(), "bad", "bad"))
	})

	t.Run("blacklists both tokens", func(t *testing.T) {
		svc, jwtSvc := newP9a8AuthService(t, &p9a8UserRepo{})
		user := p9a8User(t, "Correct1!", "ACTIVE")

		access, err := jwtSvc.GenerateAccessToken(user, "TIER_3")
		require.NoError(t, err)
		accessClaims, err := jwtSvc.ValidateAccessToken(access)
		require.NoError(t, err)

		refresh, err := jwtSvc.GenerateRefreshToken(user)
		require.NoError(t, err)
		refreshClaims, err := jwtSvc.ParseRefreshToken(refresh)
		require.NoError(t, err)

		require.NoError(t, svc.Logout(context.Background(), access, refresh))

		blacklisted, err := svc.blacklist.IsBlacklisted(context.Background(), accessClaims.ID)
		require.NoError(t, err)
		assert.True(t, blacklisted)

		blacklisted, err = svc.blacklist.IsBlacklisted(context.Background(), refreshClaims.ID)
		require.NoError(t, err)
		assert.True(t, blacklisted)
	})
}

func TestAuthServiceGetMeP9a8(t *testing.T) {
	t.Run("user not found", func(t *testing.T) {
		repo := &p9a8UserRepo{idErr: ErrUserNotFound}
		svc, _ := newP9a8AuthService(t, repo)
		_, err := svc.GetMe(context.Background(), uuid.New())
		assert.ErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("repo generic error", func(t *testing.T) {
		repo := &p9a8UserRepo{idErr: errors.New("db down")}
		svc, _ := newP9a8AuthService(t, repo)
		_, err := svc.GetMe(context.Background(), uuid.New())
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrInvalidCredentials)
	})

	t.Run("inactive account", func(t *testing.T) {
		repo := &p9a8UserRepo{idUser: p9a8User(t, "Correct1!", "SUSPENDED")}
		svc, _ := newP9a8AuthService(t, repo)
		_, err := svc.GetMe(context.Background(), uuid.New())
		assert.ErrorIs(t, err, ErrForbidden)
	})

	t.Run("success", func(t *testing.T) {
		user := p9a8User(t, "Correct1!", "ACTIVE")
		user.BaseScore = 90
		repo := &p9a8UserRepo{idUser: user}
		svc, _ := newP9a8AuthService(t, repo)

		resp, err := svc.GetMe(context.Background(), user.ID)
		require.NoError(t, err)
		assert.Equal(t, user.ID.String(), resp.UserID)
		assert.Equal(t, user.Email, resp.Email)
		assert.Equal(t, "TIER_1", resp.LoyaltyTier)
		assert.Equal(t, 120, resp.LoyaltyScore)
	})
}
