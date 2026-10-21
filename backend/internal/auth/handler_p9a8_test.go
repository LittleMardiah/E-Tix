package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func p9a8Handler(t *testing.T, repo UserRepository) (*AuthHandler, *JWTService) {
	t.Helper()
	client, _ := newTestRedisClient(t)
	jwtSvc := newTestJWTService(t)
	svc := NewAuthService(repo, jwtSvc, client, nil, NewTokenBlacklistService(client))
	return NewAuthHandler(svc, client), jwtSvc
}

func p9a8Serve(t *testing.T, method, path, body string, headers map[string]string, handler gin.HandlerFunc, setup func(*gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Handle(method, path, func(c *gin.Context) {
		if setup != nil {
			setup(c)
		}
		handler(c)
	})

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func p9a8BodyCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Error.Code
}

func TestAuthHandlerRegisterP9a8(t *testing.T) {
	payload := `{"email":"new@example.com","phone":"+6281234567890","name":"New User","password":"Abcd1234!","role":"customer"}`

	t.Run("missing idempotency key", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{})
		w := p9a8Serve(t, http.MethodPost, "/register", payload, nil, h.Register, nil)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "INVALID_REQUEST", p9a8BodyCode(t, w))
	})

	t.Run("success then replay", func(t *testing.T) {
		repo := &p9a8UserRepo{}
		h, _ := p9a8Handler(t, repo)
		headers := map[string]string{idempotencyKeyHeader: "reg-key-1"}

		first := p9a8Serve(t, http.MethodPost, "/register", payload, headers, h.Register, nil)
		require.Equal(t, http.StatusCreated, first.Code)
		require.NotNil(t, repo.created)

		second := p9a8Serve(t, http.MethodPost, "/register", payload, headers, h.Register, nil)
		assert.Equal(t, http.StatusCreated, second.Code)
		assert.Equal(t, first.Body.String(), second.Body.String())
	})

	t.Run("invalid json", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{})
		w := p9a8Serve(t, http.MethodPost, "/register", `{`, map[string]string{idempotencyKeyHeader: "k"}, h.Register, nil)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("service error", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{createErr: ErrEmailExists})
		w := p9a8Serve(t, http.MethodPost, "/register", payload, map[string]string{idempotencyKeyHeader: "k2"}, h.Register, nil)
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Equal(t, "EMAIL_EXISTS", p9a8BodyCode(t, w))
	})
}

func TestAuthHandlerLoginP9a8(t *testing.T) {
	t.Run("invalid body", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{})
		w := p9a8Serve(t, http.MethodPost, "/login", `{`, nil, h.Login, nil)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid credentials", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{emailErr: ErrUserNotFound})
		w := p9a8Serve(t, http.MethodPost, "/login", `{"email":"a@b.com","password":"x"}`, nil, h.Login, nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "UNAUTHORIZED", p9a8BodyCode(t, w))
	})

	t.Run("success", func(t *testing.T) {
		repo := &p9a8UserRepo{emailUser: p9a8User(t, "Correct1!", "ACTIVE")}
		h, _ := p9a8Handler(t, repo)
		w := p9a8Serve(t, http.MethodPost, "/login", `{"email":"alice@example.com","password":"Correct1!"}`, nil, h.Login, nil)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "access_token")
	})
}

func TestAuthHandlerRefreshTokenP9a8(t *testing.T) {
	t.Run("missing token", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{})
		w := p9a8Serve(t, http.MethodPost, "/refresh", `{}`, nil, h.RefreshToken, nil)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid token", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{})
		w := p9a8Serve(t, http.MethodPost, "/refresh", `{"refresh_token":"nope"}`, nil, h.RefreshToken, nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "UNAUTHORIZED", p9a8BodyCode(t, w))
	})

	t.Run("wrong token type", func(t *testing.T) {
		h, jwtSvc := p9a8Handler(t, &p9a8UserRepo{})
		access, err := jwtSvc.GenerateAccessToken(p9a8User(t, "Correct1!", "ACTIVE"), "TIER_3")
		require.NoError(t, err)
		w := p9a8Serve(t, http.MethodPost, "/refresh", `{"refresh_token":"`+access+`"}`, nil, h.RefreshToken, nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "TOKEN_TYPE_MISMATCH", p9a8BodyCode(t, w))
	})

	t.Run("inactive account", func(t *testing.T) {
		user := p9a8User(t, "Correct1!", "SUSPENDED")
		h, jwtSvc := p9a8Handler(t, &p9a8UserRepo{idUser: user})
		refresh, err := jwtSvc.GenerateRefreshToken(user)
		require.NoError(t, err)
		w := p9a8Serve(t, http.MethodPost, "/refresh", `{"refresh_token":"`+refresh+`"}`, nil, h.RefreshToken, nil)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, "FORBIDDEN", p9a8BodyCode(t, w))
	})

	t.Run("internal error", func(t *testing.T) {
		h, jwtSvc := p9a8Handler(t, &p9a8UserRepo{idErr: errors.New("db down")})
		refresh, err := jwtSvc.GenerateRefreshToken(p9a8User(t, "Correct1!", "ACTIVE"))
		require.NoError(t, err)
		w := p9a8Serve(t, http.MethodPost, "/refresh", `{"refresh_token":"`+refresh+`"}`, nil, h.RefreshToken, nil)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("blacklisted replay", func(t *testing.T) {
		user := p9a8User(t, "Correct1!", "ACTIVE")
		h, jwtSvc := p9a8Handler(t, &p9a8UserRepo{idUser: user})
		refresh, err := jwtSvc.GenerateRefreshToken(user)
		require.NoError(t, err)
		body := `{"refresh_token":"` + refresh + `"}`

		first := p9a8Serve(t, http.MethodPost, "/refresh", body, nil, h.RefreshToken, nil)
		require.Equal(t, http.StatusOK, first.Code)

		second := p9a8Serve(t, http.MethodPost, "/refresh", body, nil, h.RefreshToken, nil)
		assert.Equal(t, http.StatusUnauthorized, second.Code)
		assert.Equal(t, "TOKEN_BLACKLISTED", p9a8BodyCode(t, second))
	})

	t.Run("success", func(t *testing.T) {
		user := p9a8User(t, "Correct1!", "ACTIVE")
		h, jwtSvc := p9a8Handler(t, &p9a8UserRepo{idUser: user})
		refresh, err := jwtSvc.GenerateRefreshToken(user)
		require.NoError(t, err)
		w := p9a8Serve(t, http.MethodPost, "/refresh", `{"refresh_token":"`+refresh+`"}`, nil, h.RefreshToken, nil)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "access_token")
	})
}

func TestAuthHandlerLogoutP9a8(t *testing.T) {
	t.Run("empty request", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{})
		w := p9a8Serve(t, http.MethodPost, "/logout", `{}`, nil, h.Logout, nil)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("valid tokens", func(t *testing.T) {
		h, jwtSvc := p9a8Handler(t, &p9a8UserRepo{})
		user := p9a8User(t, "Correct1!", "ACTIVE")
		access, err := jwtSvc.GenerateAccessToken(user, "TIER_3")
		require.NoError(t, err)
		refresh, err := jwtSvc.GenerateRefreshToken(user)
		require.NoError(t, err)

		w := p9a8Serve(t, http.MethodPost, "/logout",
			`{"refresh_token":"`+refresh+`"}`,
			map[string]string{"Authorization": "Bearer " + access},
			h.Logout, nil)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestAuthHandlerGetMeP9a8(t *testing.T) {
	withUserID := func(id string) func(*gin.Context) {
		return func(c *gin.Context) { c.Set("user_id", id) }
	}

	t.Run("missing context", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{})
		w := p9a8Serve(t, http.MethodGet, "/me", "", nil, h.GetMe, nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("invalid uuid", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{})
		w := p9a8Serve(t, http.MethodGet, "/me", "", nil, h.GetMe, withUserID("not-a-uuid"))
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("not found", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{idErr: ErrUserNotFound})
		w := p9a8Serve(t, http.MethodGet, "/me", "", nil, h.GetMe, withUserID(uuid.NewString()))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("inactive", func(t *testing.T) {
		repo := &p9a8UserRepo{idUser: p9a8User(t, "Correct1!", "SUSPENDED")}
		h, _ := p9a8Handler(t, repo)
		w := p9a8Serve(t, http.MethodGet, "/me", "", nil, h.GetMe, withUserID(uuid.NewString()))
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("internal error", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{idErr: errors.New("db down")})
		w := p9a8Serve(t, http.MethodGet, "/me", "", nil, h.GetMe, withUserID(uuid.NewString()))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("success", func(t *testing.T) {
		user := p9a8User(t, "Correct1!", "ACTIVE")
		h, _ := p9a8Handler(t, &p9a8UserRepo{idUser: user})
		w := p9a8Serve(t, http.MethodGet, "/me", "", nil, h.GetMe, withUserID(user.ID.String()))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "loyalty_tier")
	})
}

func TestAuthHandlerVerifyOTPP9a8(t *testing.T) {
	withUserID := func(id string) func(*gin.Context) {
		return func(c *gin.Context) { c.Set("user_id", id) }
	}

	t.Run("invalid body", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{})
		w := p9a8Serve(t, http.MethodPost, "/verify", `{`, nil, h.VerifyOTP, nil)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("user mismatch", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{})
		w := p9a8Serve(t, http.MethodPost, "/verify", `{"user_id":"`+uuid.NewString()+`","code":"123456"}`, nil, h.VerifyOTP, withUserID(uuid.NewString()))
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("invalid uuid", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{})
		w := p9a8Serve(t, http.MethodPost, "/verify", `{"user_id":"bad","code":"123456"}`, nil, h.VerifyOTP, withUserID("bad"))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("expired otp", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{})
		uid := uuid.NewString()
		w := p9a8Serve(t, http.MethodPost, "/verify", `{"user_id":"`+uid+`","code":"123456"}`, nil, h.VerifyOTP, withUserID(uid))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "INVALID_REQUEST", p9a8BodyCode(t, w))
	})

	t.Run("invalid otp", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{})
		uid := uuid.NewString()
		w := p9a8Serve(t, http.MethodPost, "/verify", `{"user_id":"`+uid+`","code":"12"}`, nil, h.VerifyOTP, withUserID(uid))
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "UNAUTHORIZED", p9a8BodyCode(t, w))
	})

	t.Run("success", func(t *testing.T) {
		h, _ := p9a8Handler(t, &p9a8UserRepo{})
		uid := uuid.New()
		code, err := h.svc.GenerateOTP(t.Context(), uid, "alice@example.com")
		require.NoError(t, err)

		w := p9a8Serve(t, http.MethodPost, "/verify",
			`{"user_id":"`+uid.String()+`","code":"`+code+`"}`,
			nil, h.VerifyOTP, withUserID(uid.String()))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "email_verified")
	})
}
