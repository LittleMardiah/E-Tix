package auth

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID              uuid.UUID  `json:"user_id"`
	Email           string     `json:"email"`
	Phone           string     `json:"phone"`
	Name            string     `json:"name"`
	PasswordHash    string     `json:"-"`
	Role            string     `json:"role"`
	Status          string     `json:"status"`
	BaseScore       int        `json:"base_score"`
	TotalPurchases  int        `json:"total_purchases"`
	FirstPurchaseAt *time.Time `json:"first_purchase_at,omitempty"`
	LastPurchaseAt  *time.Time `json:"last_purchase_at,omitempty"`
	ProfilePhotoURL string     `json:"profile_photo_url,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type Claims struct {
	UserID    string `json:"sub"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Tier      string `json:"tier"`
	TokenType string `json:"token_type"`
	JTI       string `json:"jti"`
	Exp       int64  `json:"exp"`
	Iat       int64  `json:"iat"`
	Iss       string `json:"iss"`
}

type RegisterRequest struct {
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Name     string `json:"name"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type VerifyOTPRequest struct {
	UserID string `json:"user_id"`
	Code   string `json:"code"`
}

type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type LogoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type RegisterResponse struct {
	UserID       string `json:"user_id"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type LoginResponse struct {
	UserID       string `json:"user_id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	Tier         string `json:"tier"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type UserMeResponse struct {
	UserID          string     `json:"user_id"`
	Email           string     `json:"email"`
	Phone           string     `json:"phone"`
	Name            string     `json:"name"`
	Role            string     `json:"role"`
	Status          string     `json:"status"`
	BaseScore       int        `json:"base_score"`
	TotalPurchases  int        `json:"total_purchases"`
	LoyaltyTier     string     `json:"loyalty_tier"`
	LoyaltyScore    int        `json:"loyalty_score"`
	FirstPurchaseAt *time.Time `json:"first_purchase_at,omitempty"`
	LastPurchaseAt  *time.Time `json:"last_purchase_at,omitempty"`
	ProfilePhotoURL string     `json:"profile_photo_url,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}
