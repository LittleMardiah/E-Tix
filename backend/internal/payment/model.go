package payment

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ============================================================
// Constants
// ============================================================

const (
	// ModeSplit: each member pays own share (1 obligation per member).
	ModeSplit = "SPLIT"
	// ModeBulk: leader pays all (1 obligation, seat_position NULL).
	ModeBulk = "BULK"
)

// Payment status values mirror payment_status_enum (DATABASE_SCHEMA v2.9).
const (
	StatusPending   = "PENDING"
	StatusPaid      = "PAID"
	StatusExpired   = "EXPIRED"
	StatusFailed    = "FAILED"
	StatusRefunded  = "REFUNDED"
	StatusCancelled = "CANCELLED"
)

// Payment methods mirror payment_method_enum.
const (
	MethodVA         = "VA"
	MethodEWallet    = "E_WALLET"
	MethodCreditCard = "CREDIT_CARD"
)

// ============================================================
// Domain errors
// ============================================================

var (
	ErrBookingNotFound       = errors.New("payment: booking not found")
	ErrBookingNotPending     = errors.New("payment: booking is not pending")
	ErrNotLeader             = errors.New("payment: caller is not the booking leader")
	ErrNotOwnerOrMember      = errors.New("payment: caller is neither owner nor member")
	ErrInvalidPaymentMode    = errors.New("payment: invalid payment mode")
	ErrInvalidMembers        = errors.New("payment: invalid members payload")
	ErrIdempotencyConflict   = errors.New("payment: idempotency key conflict")
	ErrWebhookInvalidSig     = errors.New("payment: webhook signature invalid")
	ErrWebhookNotFound       = errors.New("payment: webhook transaction not found")
	ErrWebhookMissingSig     = errors.New("payment: webhook signature missing")
	ErrBulkAssignPartial     = errors.New("payment: bulk assign partial failure")
	ErrTargetUserInactive    = errors.New("payment: target user inactive or suspended")
	ErrEventAlreadyStarted   = errors.New("payment: event already started")
	ErrTicketAlreadyAssigned = errors.New("payment: ticket already assigned")
	ErrObligationNotFound    = errors.New("payment: obligation not found")
	ErrObligationNotPaid     = errors.New("payment: obligation is not paid")
)

// ============================================================
// Domain structs
// ============================================================

// PaymentObligation mirrors the payment_obligations table.
type PaymentObligation struct {
	ID                    uuid.UUID  `json:"id"`
	BookingID             uuid.UUID  `json:"booking_id"`
	UserID                uuid.UUID  `json:"user_id"`
	SeatPosition          *int       `json:"seat_position,omitempty"`
	Amount                string     `json:"amount"` // NUMERIC(15,2) as string
	Status                string     `json:"status"`
	PaymentMethod         *string    `json:"payment_method,omitempty"`
	PaymentLink           *string    `json:"payment_link,omitempty"`
	ExternalTransactionID *string    `json:"external_transaction_id,omitempty"`
	WebhookReceivedAt     *time.Time `json:"webhook_received_at,omitempty"`
	IdempotencyKey        *string    `json:"idempotency_key,omitempty"`
	RefundedAt            *time.Time `json:"refunded_at,omitempty"`
	RefundTransactionID   *string    `json:"refund_transaction_id,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// ============================================================
// Request / Response — API_CONTRACT v1.2 §9.2-9.5
// ============================================================

// InitiateMember is one row in SPLIT mode.
type InitiateMember struct {
	UserID       uuid.UUID `json:"user_id"`
	SeatPosition int       `json:"seat_position"`
	Amount       string    `json:"amount"`
}

// InitiatePaymentRequest is the body for POST /bookings/{id}/payments.
type InitiatePaymentRequest struct {
	PaymentMode   string           `json:"payment_mode"` // SPLIT | BULK
	Members       []InitiateMember `json:"members,omitempty"`
	LeaderPaysAll bool             `json:"leader_pays_all,omitempty"`
	Amount        string           `json:"amount,omitempty"` // BULK only
}

// ObligationResponse is one obligation in the initiate response.
type ObligationResponse struct {
	UserID                string `json:"user_id"`
	SeatPosition          *int   `json:"seat_position,omitempty"`
	Amount                string `json:"amount"`
	Status                string `json:"status"`
	PaymentLink           string `json:"payment_link,omitempty"`
	ExternalTransactionID string `json:"external_transaction_id,omitempty"`
}

// InitiatePaymentResponse is the response for POST /bookings/{id}/payments.
type InitiatePaymentResponse struct {
	BookingID     string               `json:"booking_id"`
	PaymentStatus string               `json:"payment_status"`
	Obligations   []ObligationResponse `json:"obligations"`
	ExpiresAt     string               `json:"expires_at"`
}

// BookingStatusResponse is the response for GET /bookings/{id}/status.
type BookingStatusResponse struct {
	BookingID        string               `json:"booking_id"`
	PaymentStatus    string               `json:"payment_status"`
	Obligations      []ObligationResponse `json:"obligations"`
	TicketsGenerated bool                 `json:"tickets_generated"`
}

// BulkAssignEntry is one assignment row.
type BulkAssignEntry struct {
	TicketID uuid.UUID `json:"ticket_id"`
	UserID   uuid.UUID `json:"user_id"`
}

// BulkAssignRequest is the body for POST /payments/bulk/assign.
type BulkAssignRequest struct {
	BookingID   uuid.UUID         `json:"booking_id"`
	Assignments []BulkAssignEntry `json:"assignments"`
}

// BulkAssignTicketResult is one row in the bulk assign response.
type BulkAssignTicketResult struct {
	TicketID   string `json:"ticket_id"`
	AssignedTo string `json:"assigned_to,omitempty"`
	Status     string `json:"status"` // SUCCESS | FAILED
	Error      string `json:"error,omitempty"`
}

// BulkAssignResponse is the response for POST /payments/bulk/assign.
type BulkAssignResponse struct {
	BookingID string                   `json:"booking_id"`
	Assigned  int                      `json:"assigned"`
	Tickets   []BulkAssignTicketResult `json:"tickets"`
}

// WebhookPayload is the body for POST /webhook/payment.
type WebhookPayload struct {
	ExternalTransactionID string `json:"external_transaction_id"`
	Status                string `json:"status"`
	Amount                string `json:"amount"`
	Signature             string `json:"signature,omitempty"`
}
