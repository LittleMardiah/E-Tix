package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestValidateRegisterRequestP9a8(t *testing.T) {
	valid := func() *RegisterRequest {
		return &RegisterRequest{
			Email:    "alice@example.com",
			Phone:    "+6281234567890",
			Name:     "Alice",
			Password: "Abcd1234!",
			Role:     "customer",
		}
	}

	cases := []struct {
		name      string
		mutate    func(*RegisterRequest)
		field     string
		wantError bool
	}{
		{name: "valid customer", mutate: func(*RegisterRequest) {}},
		{name: "valid empty role defaults later", mutate: func(r *RegisterRequest) { r.Role = "" }},
		{name: "valid organizer", mutate: func(r *RegisterRequest) { r.Role = "event_organizer" }},
		{name: "valid gate staff", mutate: func(r *RegisterRequest) { r.Role = "gate_staff" }},
		{name: "bad email", mutate: func(r *RegisterRequest) { r.Email = "not-an-email" }, field: "email", wantError: true},
		{name: "empty email", mutate: func(r *RegisterRequest) { r.Email = "" }, field: "email", wantError: true},
		{name: "bad phone", mutate: func(r *RegisterRequest) { r.Phone = "123" }, field: "phone", wantError: true},
		{name: "phone with letters", mutate: func(r *RegisterRequest) { r.Phone = "0812abc4567" }, field: "phone", wantError: true},
		{name: "short name", mutate: func(r *RegisterRequest) { r.Name = "ab" }, field: "name", wantError: true},
		{name: "blank name", mutate: func(r *RegisterRequest) { r.Name = "   " }, field: "name", wantError: true},
		{name: "long name", mutate: func(r *RegisterRequest) { r.Name = strings.Repeat("a", 256) }, field: "name", wantError: true},
		{name: "short password", mutate: func(r *RegisterRequest) { r.Password = "Ab1!" }, field: "password", wantError: true},
		{name: "weak password", mutate: func(r *RegisterRequest) { r.Password = "abcdefgh" }, field: "password", wantError: true},
		{name: "disallowed role", mutate: func(r *RegisterRequest) { r.Role = "admin" }, field: "role", wantError: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := valid()
			tc.mutate(req)
			err := validateRegisterRequest(req)
			if !tc.wantError {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var vErr *ValidationError
			require.ErrorAs(t, err, &vErr)
			assert.Equal(t, tc.field, vErr.Field)
		})
	}
}

func TestMapUserWriteErrorP9a8(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		target error
	}{
		{name: "email unique violation", err: &pgconn.PgError{Code: "23505", ConstraintName: "users_email_key"}, target: ErrEmailExists},
		{name: "phone unique violation", err: &pgconn.PgError{Code: "23505", ConstraintName: "users_phone_key"}, target: ErrPhoneExists},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.ErrorIs(t, mapUserWriteError(tc.err), tc.target)
		})
	}

	otherUnique := &pgconn.PgError{Code: "23505", ConstraintName: "users_other_key"}
	assert.Same(t, otherUnique, mapUserWriteError(otherUnique))

	fkViolation := &pgconn.PgError{Code: "23503", ConstraintName: "users_email_key"}
	assert.Same(t, fkViolation, mapUserWriteError(fkViolation))

	plain := errors.New("boom")
	assert.Same(t, plain, mapUserWriteError(plain))
	assert.NoError(t, mapUserWriteError(nil))
}

func TestMaskEmailP9a8(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "normal", input: "alice@example.com", want: "a***@example.com"},
		{name: "single char local", input: "a@example.com", want: "a***@example.com"},
		{name: "no at", input: "alice.example.com", want: "***"},
		{name: "empty", input: "", want: "***"},
		{name: "at at start", input: "@example.com", want: "***"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, maskEmail(tc.input))
		})
	}
}

func TestLogNotificationSendOTPP9a8(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	n := NewLogNotification(zap.New(core))
	require.NotNil(t, n)

	userID := uuid.New()
	require.NoError(t, n.SendOTP(context.Background(), userID, "alice@example.com", "123456"))

	require.Equal(t, 1, logs.Len())
	entry := logs.All()[0]
	assert.Equal(t, "OTP sent (dev mode)", entry.Message)
	fields := entry.ContextMap()
	assert.Equal(t, userID.String(), fields["user_id"])
	assert.Equal(t, "a***@example.com", fields["email"])
	assert.Equal(t, "123456", fields["code"])
}

type p9a8OTPStub struct {
	calls int
	err   error
}

func (s *p9a8OTPStub) SendOTP(_ context.Context, _ uuid.UUID, _ string, _ string) error {
	s.calls++
	return s.err
}

func TestGenerateOTPP9a8(t *testing.T) {
	t.Run("nil redis", func(t *testing.T) {
		svc := NewAuthService(nil, nil, nil, nil, nil)
		_, err := svc.GenerateOTP(context.Background(), uuid.New(), "alice@example.com")
		require.Error(t, err)
	})

	t.Run("stores code and notifies", func(t *testing.T) {
		client, _ := newTestRedisClient(t)
		stub := &p9a8OTPStub{}
		svc := NewAuthService(nil, nil, client, stub, nil)
		userID := uuid.New()

		code, err := svc.GenerateOTP(context.Background(), userID, "alice@example.com")
		require.NoError(t, err)
		assert.Len(t, code, OTPCodeLength)
		assert.True(t, isNumeric(code))
		assert.Equal(t, 1, stub.calls)

		require.NoError(t, svc.VerifyOTP(context.Background(), userID, code))
	})

	t.Run("notification failure is non-fatal", func(t *testing.T) {
		client, _ := newTestRedisClient(t)
		stub := &p9a8OTPStub{err: errors.New("provider down")}
		svc := NewAuthService(nil, nil, client, stub, nil)
		userID := uuid.New()

		code, err := svc.GenerateOTP(context.Background(), userID, "alice@example.com")
		require.NoError(t, err)
		assert.Len(t, code, OTPCodeLength)
		assert.Equal(t, 1, stub.calls)
	})
}

func TestNewJWTServiceWithKeysP9a8(t *testing.T) {
	t.Run("nil keys", func(t *testing.T) {
		svc := NewJWTServiceWithKeys(nil, nil, "")
		require.NotNil(t, svc)
		assert.Equal(t, "e-tix", svc.issuer)
		assert.Nil(t, svc.priv)
		assert.Nil(t, svc.pub)
	})

	t.Run("valid keys", func(t *testing.T) {
		key := generateTestRSAKey(t)
		svc := NewJWTServiceWithKeys(key, &key.PublicKey, "")
		require.NotNil(t, svc)
		assert.Equal(t, "e-tix", svc.issuer)
		assert.Same(t, key, svc.priv)

		user := newTestUser()
		refresh, err := svc.GenerateRefreshToken(user)
		require.NoError(t, err)

		claims, err := svc.ValidateRefreshToken(refresh)
		require.NoError(t, err)
		assert.Equal(t, user.ID.String(), claims.UserID)
		assert.Equal(t, "refresh", claims.TokenType)
	})
}

func TestValidateRefreshTokenP9a8(t *testing.T) {
	svc := newTestJWTService(t)
	user := newTestUser()

	refresh, err := svc.GenerateRefreshToken(user)
	require.NoError(t, err)

	claims, err := svc.ValidateRefreshToken(refresh)
	require.NoError(t, err)
	assert.Equal(t, user.ID.String(), claims.UserID)

	access, err := svc.GenerateAccessToken(user, "TIER_3")
	require.NoError(t, err)
	_, err = svc.ValidateRefreshToken(access)
	assert.ErrorIs(t, err, ErrTokenTypeMismatch)

	_, err = svc.ValidateRefreshToken("not-a-token")
	assert.Error(t, err)
}

func newP9a8GinContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	return c, w
}

func responseErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Error.Code
}

func TestWriteRegisterErrorP9a8(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "validation", err: &ValidationError{Field: "email", Message: "bad"}, wantStatus: http.StatusBadRequest, wantCode: "INVALID_REQUEST"},
		{name: "email exists", err: ErrEmailExists, wantStatus: http.StatusConflict, wantCode: "EMAIL_EXISTS"},
		{name: "phone exists", err: ErrPhoneExists, wantStatus: http.StatusConflict, wantCode: "PHONE_EXISTS"},
		{name: "internal", err: errors.New("boom"), wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, w := newP9a8GinContext()
			writeRegisterError(c, tc.err)
			assert.Equal(t, tc.wantStatus, w.Code)
			assert.Equal(t, tc.wantCode, responseErrorCode(t, w))
		})
	}
}

func TestWriteLoginErrorP9a8(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid credentials", err: ErrInvalidCredentials, wantStatus: http.StatusUnauthorized, wantCode: "UNAUTHORIZED"},
		{name: "validation", err: &ValidationError{Field: "email", Message: "bad"}, wantStatus: http.StatusBadRequest, wantCode: "INVALID_REQUEST"},
		{name: "forbidden", err: ErrForbidden, wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN"},
		{name: "internal", err: errors.New("boom"), wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, w := newP9a8GinContext()
			writeLoginError(c, tc.err)
			assert.Equal(t, tc.wantStatus, w.Code)
			assert.Equal(t, tc.wantCode, responseErrorCode(t, w))
		})
	}
}
