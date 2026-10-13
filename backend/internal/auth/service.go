package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
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
	repo UserRepository
	jwt  *JWTService
}

func NewAuthService(repo UserRepository, jwt *JWTService) *AuthService {
	return &AuthService{repo: repo, jwt: jwt}
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
