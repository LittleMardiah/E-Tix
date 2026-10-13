package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const (
	bcryptCost        = 12
	registerExpiresIn = int64(3600)
	defaultUserRole   = "customer"
	defaultUserStatus = "ACTIVE"

	minPasswordLength = 8
	maxPasswordLength = 128
	minNameLength     = 3
	maxNameLength     = 255
)

var (
	emailPattern = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)
	phonePattern = regexp.MustCompile(`^\+?[0-9]{10,15}$`)

	allowedRegisterRoles = map[string]struct{}{
		"customer":        {},
		"event_organizer": {},
		"gate_staff":      {},
	}
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

type AuthService struct {
	repo      UserRepository
	jwt       *JWTService
	rdb       *redis.Client
	otp       OTPNotification
	blacklist *TokenBlacklistService
}

func NewAuthService(repo UserRepository, jwt *JWTService, rdb *redis.Client, otp OTPNotification, blacklist *TokenBlacklistService) *AuthService {
	return &AuthService{repo: repo, jwt: jwt, rdb: rdb, otp: otp, blacklist: blacklist}
}

func (s *AuthService) Register(ctx context.Context, req *RegisterRequest) (*RegisterResponse, error) {
	if req == nil {
		return nil, &ValidationError{Field: "body", Message: "request body is required"}
	}

	if err := validateRegisterRequest(req); err != nil {
		return nil, err
	}

	role := req.Role
	if role == "" {
		role = defaultUserRole
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("auth: hash password: %w", err)
	}

	user := &User{
		Email:        req.Email,
		Phone:        req.Phone,
		Name:         req.Name,
		PasswordHash: string(hash),
		Role:         role,
		Status:       defaultUserStatus,
	}

	if err := s.repo.CreateUser(ctx, user); err != nil {
		if errors.Is(err, ErrEmailExists) || errors.Is(err, ErrPhoneExists) {
			return nil, err
		}
		return nil, fmt.Errorf("auth: create user: %w", err)
	}

	accessToken, err := s.jwt.GenerateAccessToken(user, "TIER_3")
	if err != nil {
		return nil, fmt.Errorf("auth: generate access token: %w", err)
	}

	refreshToken, err := s.jwt.GenerateRefreshToken(user)
	if err != nil {
		return nil, fmt.Errorf("auth: generate refresh token: %w", err)
	}

	return &RegisterResponse{
		UserID:       user.ID.String(),
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    registerExpiresIn,
	}, nil
}

func validateRegisterRequest(req *RegisterRequest) error {
	if !emailPattern.MatchString(req.Email) {
		return &ValidationError{Field: "email", Message: "invalid email format"}
	}

	if !phonePattern.MatchString(req.Phone) {
		return &ValidationError{Field: "phone", Message: "invalid phone format"}
	}

	nameLen := len(strings.TrimSpace(req.Name))
	if nameLen < minNameLength || nameLen > maxNameLength {
		return &ValidationError{Field: "name", Message: "name must be between 3 and 255 characters"}
	}

	if err := validatePassword(req.Password); err != nil {
		return err
	}

	if req.Role != "" {
		if _, ok := allowedRegisterRoles[req.Role]; !ok {
			return &ValidationError{Field: "role", Message: "role is not allowed for self-registration"}
		}
	}

	return nil
}

func validatePassword(password string) error {
	if len(password) < minPasswordLength || len(password) > maxPasswordLength {
		return &ValidationError{Field: "password", Message: "password must be between 8 and 128 characters"}
	}

	var hasUpper, hasLower, hasDigit, hasSymbol bool
	for _, r := range password {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= '0' && r <= '9':
			hasDigit = true
		default:
			hasSymbol = true
		}
	}

	if !hasUpper || !hasLower || !hasDigit || !hasSymbol {
		return &ValidationError{Field: "password", Message: "password must contain upper, lower, number, and symbol"}
	}

	return nil
}

var (
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrForbidden          = errors.New("auth: forbidden")
	ErrTokenBlacklisted   = errors.New("auth: token blacklisted")
)

const loginExpiresIn = int64(3600)

func (s *AuthService) Login(ctx context.Context, req *LoginRequest) (*LoginResponse, error) {
	if req == nil {
		return nil, &ValidationError{Field: "body", Message: "request body is required"}
	}

	if strings.TrimSpace(req.Email) == "" {
		return nil, &ValidationError{Field: "email", Message: "email is required"}
	}
	if req.Password == "" {
		return nil, &ValidationError{Field: "password", Message: "password is required"}
	}

	user, err := s.repo.GetUserByEmail(ctx, req.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("auth: get user by email: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	if user.Status != "ACTIVE" {
		return nil, ErrForbidden
	}

	tier := computeTier(user.BaseScore, user.TotalPurchases)

	accessToken, err := s.jwt.GenerateAccessToken(user, tier)
	if err != nil {
		return nil, fmt.Errorf("auth: generate access token: %w", err)
	}

	refreshToken, err := s.jwt.GenerateRefreshToken(user)
	if err != nil {
		return nil, fmt.Errorf("auth: generate refresh token: %w", err)
	}

	return &LoginResponse{
		UserID:       user.ID.String(),
		Email:        user.Email,
		Name:         user.Name,
		Role:         user.Role,
		Tier:         tier,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    loginExpiresIn,
	}, nil
}

func (s *AuthService) RefreshToken(ctx context.Context, refreshToken string) (*RefreshTokenResponse, error) {
	claims, err := s.jwt.ParseRefreshToken(refreshToken)
	if err != nil {
		if errors.Is(err, ErrTokenTypeMismatch) {
			return nil, ErrTokenTypeMismatch
		}
		return nil, ErrInvalidCredentials
	}

	blacklisted, err := s.blacklist.IsBlacklisted(ctx, claims.ID)
	if err != nil {
		return nil, fmt.Errorf("auth: check blacklist: %w", err)
	}
	if blacklisted {
		return nil, ErrTokenBlacklisted
	}

	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("auth: get user by id: %w", err)
	}

	if user.Status != "ACTIVE" {
		return nil, ErrForbidden
	}

	if err := s.blacklist.AddToBlacklist(ctx, claims.ID, time.Until(claims.ExpiresAt.Time)); err != nil {
		return nil, fmt.Errorf("auth: blacklist old refresh token: %w", err)
	}

	tier := computeTier(user.BaseScore, user.TotalPurchases)

	accessToken, err := s.jwt.GenerateAccessToken(user, tier)
	if err != nil {
		return nil, fmt.Errorf("auth: generate access token: %w", err)
	}

	newRefreshToken, err := s.jwt.GenerateRefreshToken(user)
	if err != nil {
		return nil, fmt.Errorf("auth: generate refresh token: %w", err)
	}

	return &RefreshTokenResponse{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		ExpiresIn:    loginExpiresIn,
	}, nil
}

func (s *AuthService) Logout(ctx context.Context, accessToken string, refreshToken string) error {
	if accessToken != "" {
		if claims, err := s.jwt.ValidateAccessToken(accessToken); err == nil {
			if bErr := s.blacklist.AddToBlacklist(ctx, claims.ID, time.Until(claims.ExpiresAt.Time)); bErr != nil {
				zap.L().Warn("failed to blacklist access token", zap.String("jti", claims.ID), zap.Error(bErr))
			}
		}
	}

	if refreshToken != "" {
		if claims, err := s.jwt.ParseRefreshToken(refreshToken); err == nil {
			if bErr := s.blacklist.AddToBlacklist(ctx, claims.ID, time.Until(claims.ExpiresAt.Time)); bErr != nil {
				zap.L().Warn("failed to blacklist refresh token", zap.String("jti", claims.ID), zap.Error(bErr))
			}
		}
	}

	return nil
}

func computeTier(baseScore, totalPurchases int) string {
	score := baseScore + totalPurchases*10
	switch {
	case score >= 90:
		return "TIER_1"
	case score >= 70:
		return "TIER_2"
	default:
		return "TIER_3"
	}
}

const (
	OTPCodeLength = 6
	OTPTTL        = 5 * time.Minute

	otpKeyPrefix = "otp:"
)

var (
	ErrOTPExpired = errors.New("auth: otp expired or not found")
	ErrOTPInvalid = errors.New("auth: invalid otp code")
)

func otpKey(userID uuid.UUID) string {
	return otpKeyPrefix + userID.String()
}

func generateOTPCode() (string, error) {
	codeSpace := int64(1)
	for i := 0; i < OTPCodeLength; i++ {
		codeSpace *= 10
	}

	n, err := rand.Int(rand.Reader, big.NewInt(codeSpace))
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%0*d", OTPCodeLength, n.Int64()), nil
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// GenerateOTP creates a 6-digit OTP, stores it in Redis with a 5 minute TTL,
// and attempts to notify the user. A notification failure is logged but does
// not fail OTP generation. The code is returned for dev/testing.
func (s *AuthService) GenerateOTP(ctx context.Context, userID uuid.UUID, email string) (string, error) {
	if s.rdb == nil {
		return "", errors.New("auth: redis client is not configured")
	}

	code, err := generateOTPCode()
	if err != nil {
		return "", fmt.Errorf("auth: generate otp code: %w", err)
	}

	if err := s.rdb.Set(ctx, otpKey(userID), code, OTPTTL).Err(); err != nil {
		return "", fmt.Errorf("auth: store otp: %w", err)
	}

	if s.otp != nil {
		if err := s.otp.SendOTP(ctx, userID, email, code); err != nil {
			zap.L().Warn("failed to send otp notification",
				zap.String("user_id", userID.String()),
				zap.Error(err),
			)
		}
	}

	return code, nil
}

// VerifyOTP validates a user-supplied OTP against the value stored in Redis.
// On success the code is deleted so it cannot be reused.
func (s *AuthService) VerifyOTP(ctx context.Context, userID uuid.UUID, code string) error {
	if len(code) != OTPCodeLength || !isNumeric(code) {
		return ErrOTPInvalid
	}

	if s.rdb == nil {
		return errors.New("auth: redis client is not configured")
	}

	stored, err := s.rdb.GetString(ctx, otpKey(userID))
	if errors.Is(err, goredis.Nil) {
		return ErrOTPExpired
	}
	if err != nil {
		return fmt.Errorf("auth: get otp: %w", err)
	}
	if stored == "" {
		return ErrOTPExpired
	}

	if subtle.ConstantTimeCompare([]byte(stored), []byte(code)) != 1 {
		return ErrOTPInvalid
	}

	if err := s.rdb.Del(ctx, otpKey(userID)).Err(); err != nil {
		return fmt.Errorf("auth: delete otp: %w", err)
	}

	return nil
}
