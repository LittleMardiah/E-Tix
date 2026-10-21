package admin

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// FASE 8 P8a — admin module data model.
// Mirrors API_CONTRACT §14 + DATABASE_SCHEMA v2.9 (admin_actions, blacklist).
// Pure data structures. Business logic lands in E3-E9.

// ---- Sentinel errors (handler maps to HTTP per API_CONTRACT §3.2) ----

var (
	ErrAdminNotFound      = errors.New("admin: not found")
	ErrAdminForbidden     = errors.New("admin: forbidden")
	ErrAdminConflict      = errors.New("admin: conflict")
	ErrAdminUnprocessable = errors.New("admin: unprocessable")

	// 2FA middleware errors (E2)
	ErrAdmin2FARequired    = errors.New("admin: X-Admin-2FA-Token header missing")
	ErrAdmin2FAInvalid     = errors.New("admin: invalid 2FA token")
	ErrAdmin2FALocked      = errors.New("admin: 2FA lockout active")
	ErrAdmin2FANotEnrolled = errors.New("admin: 2FA not enrolled")
)

// ---- Requests ----

// BulkCancelRequest = POST /admin/events/{event_id}/bulk-cancel body (§14.1)
type BulkCancelRequest struct {
	Reason string `json:"reason" binding:"required"`
}

// LedgerReverseRequest = POST /admin/ledger/reverse body (§14.3)
type LedgerReverseRequest struct {
	TransactionID uuid.UUID `json:"transaction_id" binding:"required"`
	Reason        string    `json:"reason"         binding:"required"`
}

// ForceReleaseRequest = POST /admin/force-release body (§14.5)
type ForceReleaseRequest struct {
	BookingID uuid.UUID `json:"booking_id" binding:"required"`
	Reason    string    `json:"reason"     binding:"required"`
}

// AddBlacklistRequest = POST /admin/blacklist body (§14.8)
type AddBlacklistRequest struct {
	Type      string     `json:"type"     binding:"required"` // ip | device_fingerprint
	Value     string     `json:"value"    binding:"required"`
	Reason    string     `json:"reason"   binding:"required"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"` // null = permanent
}

// SuspendUserRequest = PATCH /admin/users/{user_id}/suspend body (§14.10)
type SuspendUserRequest struct {
	Reason string `json:"reason" binding:"required"`
}

// ---- Responses ----

// BulkCancelResponse = 202 (§14.1)
type BulkCancelResponse struct {
	EventID          uuid.UUID `json:"event_id"`
	CancelledTickets int       `json:"cancelled_tickets"`
	RefundTotal      string    `json:"refund_total"`
	Status           string    `json:"status"` // "PROCESSING"
}

// LedgerResponse = GET /admin/ledger (§14.2)
type LedgerResponse struct {
	Entries []LedgerEntry `json:"entries"`
	Summary LedgerSummary `json:"summary"`
}

// LedgerEntry = one row from event-sourced view (payment_obligations +
// resale_transactions + admin_actions UNION, per API_CONTRACT §14.2).
type LedgerEntry struct {
	EventID              uuid.UUID  `json:"event_id"`
	EventType            string     `json:"event_type"`     // PAYMENT_PAID / RESALE_COMPLETED / ADMIN_*
	ReferenceType        string     `json:"reference_type"` // payment_obligation | resale_transaction | admin_action | booking
	ReferenceID          uuid.UUID  `json:"reference_id"`
	BookingID            *uuid.UUID `json:"booking_id,omitempty"`
	UserID               *uuid.UUID `json:"user_id,omitempty"`
	Amount               string     `json:"amount"`
	Status               string     `json:"status"`
	GatewayTransactionID *string    `json:"gateway_transaction_id,omitempty"`
	AdminID              *uuid.UUID `json:"admin_id,omitempty"`
	Reconciled           bool       `json:"reconciled"`
	CreatedAt            time.Time  `json:"created_at"`
}

// LedgerSummary = aggregation block (§14.2)
type LedgerSummary struct {
	TotalPaid         string `json:"total_paid"`
	TotalPending      string `json:"total_pending"`
	TotalRefunded     string `json:"total_refunded"`
	UnreconciledCount int    `json:"unreconciled_count"`
}

// LedgerReverseResponse = 200 (§14.3)
type LedgerReverseResponse struct {
	PaymentObligationID        uuid.UUID `json:"payment_obligation_id"`
	Status                     string    `json:"status"` // "REFUNDED"
	RefundReason               string    `json:"refund_reason"`
	GatewayRefundTransactionID string    `json:"gateway_refund_transaction_id"`
	AdminActionID              uuid.UUID `json:"admin_action_id"`
	RefundedAt                 time.Time `json:"refunded_at"`
}

// StuckLocksResponse = GET /admin/stuck-locks (§14.4)
type StuckLocksResponse struct {
	StuckLocks []StuckLock `json:"stuck_locks"`
}

// StuckLock = one booking with expired lock (§14.4)
type StuckLock struct {
	BookingID       uuid.UUID `json:"booking_id"`
	EventID         uuid.UUID `json:"event_id"` // F8-F02: needed for Redis key
	SectionID       uuid.UUID `json:"section_id"`
	SectionName     string    `json:"section_name"`
	LayoutVersion   int       `json:"layout_version"` // F8-F02: needed for Redis key
	SeatPositions   []int     `json:"seat_positions"`
	LockedSince     time.Time `json:"locked_since"`
	DurationMinutes int       `json:"duration_minutes"`
	Status          string    `json:"status"` // "STUCK"
}

// ForceReleaseResponse = 200 (§14.5)
type ForceReleaseResponse struct {
	BookingID     uuid.UUID `json:"booking_id"`
	ReleasedSeats []int     `json:"released_seats"`
	NewStatus     string    `json:"new_status"` // "CANCELLED"
	ReleasedAt    time.Time `json:"released_at"`
}

// UserListResponse = GET /admin/users (§14.6)
type UserListResponse struct {
	Users []AdminUser `json:"users"`
}

// AdminUser = one row from user list (§14.6)
type AdminUser struct {
	UserID          uuid.UUID  `json:"user_id"`
	Email           string     `json:"email"`
	Phone           string     `json:"phone"`
	Name            string     `json:"name"`
	Role            string     `json:"role"`
	Status          string     `json:"status"`
	LoyaltyTier     string     `json:"loyalty_tier"`
	TotalPurchases  int        `json:"total_purchases"`
	CreatedAt       time.Time  `json:"created_at"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty"`
	SuspendedReason *string    `json:"suspended_reason,omitempty"`
}

// BlacklistListResponse = GET /admin/blacklist (§14.7)
type BlacklistListResponse struct {
	Entries []BlacklistEntry `json:"entries"`
}

// BlacklistEntry = one row (§14.7)
type BlacklistEntry struct {
	ID        uuid.UUID  `json:"id"`
	Type      string     `json:"type"` // ip | device_fingerprint
	Value     string     `json:"value"`
	Reason    *string    `json:"reason,omitempty"`
	AddedBy   *uuid.UUID `json:"added_by,omitempty"`
	AddedAt   time.Time  `json:"added_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// AddBlacklistResponse = 201 (§14.8)
type AddBlacklistResponse struct {
	ID      uuid.UUID `json:"id"`
	Type    string    `json:"type"`
	Value   string    `json:"value"`
	AddedAt time.Time `json:"added_at"`
}

// RemoveBlacklistResponse = 200 (§14.9)
type RemoveBlacklistResponse struct {
	ID        uuid.UUID `json:"id"`
	RemovedAt time.Time `json:"removed_at"`
}

// SuspendUserResponse = 200 (§14.10)
type SuspendUserResponse struct {
	UserID          uuid.UUID `json:"user_id"`
	Status          string    `json:"status"` // "SUSPENDED"
	PreviousStatus  string    `json:"previous_status"`
	SuspendedReason string    `json:"suspended_reason"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// UnsuspendUserResponse = 200 (§14.11)
type UnsuspendUserResponse struct {
	UserID         uuid.UUID `json:"user_id"`
	Status         string    `json:"status"` // "ACTIVE"
	PreviousStatus string    `json:"previous_status"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ---- Internal helper ----

// AdminActionRecord is the DB-write shape for admin_actions audit row.
// Used by repo Insert helper. Not exposed as JSON.
// Constraint: reason NOT NULL (DB), so always required.
// action_type must match admin_action_type_enum in DB:
//
//	BULK_CANCEL | MANUAL_PAID | MANUAL_REFUND | FORCE_RELEASE_SEAT |
//	UPDATE_EVENT_PRICE | SUSPEND_USER | UNSUSPEND_USER |
//	BLACKLIST_IP | REMOVE_BLACKLIST_IP
type AdminActionRecord struct {
	AdminID            uuid.UUID
	ActionType         string
	TargetUserID       *uuid.UUID
	TargetResourceType *string
	TargetResourceID   *uuid.UUID
	Description        *string
	Reason             string
	ChangesBefore      map[string]interface{} // marshaled to JSONB
	ChangesAfter       map[string]interface{} // marshaled to JSONB
	IPAddress          *string                // INET as string
	UserAgent          *string
}
