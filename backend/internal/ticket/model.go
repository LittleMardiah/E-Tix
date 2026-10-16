package ticket

import (
	"time"

	"github.com/google/uuid"
)

// Ticket mirrors the tickets table in DATABASE_SCHEMA v2.9.
// FallbackPIN is PLAIN per FLAG #1 (DATABASE_SCHEMA v2.9 authority).
type Ticket struct {
	ID                  uuid.UUID  `json:"id"`
	BookingID           uuid.UUID  `json:"booking_id"`
	PaymentObligationID *uuid.UUID `json:"payment_obligation_id,omitempty"`
	OwnerUserID         uuid.UUID  `json:"owner_user_id"`
	SectionID           uuid.UUID  `json:"section_id"`
	SeatPosition        int        `json:"seat_position"`
	Status              string     `json:"status"`
	IsAssigned          bool       `json:"is_assigned"`
	QRSecretKey         string     `json:"-"`
	LastQRGeneratedAt   time.Time  `json:"last_qr_generated_at"`
	FallbackPIN         string     `json:"fallback_pin"`
	FallbackPINUsed     bool       `json:"fallback_pin_used"`
	FallbackPINUsedAt   *time.Time `json:"fallback_pin_used_at,omitempty"`
	OriginalOwnerUserID *uuid.UUID `json:"original_owner_user_id,omitempty"`
	ResaleTransactionID *uuid.UUID `json:"resale_transaction_id,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// TicketListItem is the shape used in GET /tickets list responses.
type TicketListItem struct {
	ID           uuid.UUID `json:"id"`
	EventID      uuid.UUID `json:"event_id"`
	EventName    string    `json:"event_name"`
	SectionName  string    `json:"section_name"`
	SeatPosition int       `json:"seat_position"`
	Status       string    `json:"status"`
	FallbackPIN  string    `json:"fallback_pin"`
	CreatedAt    time.Time `json:"created_at"`
}

// TicketDetail is the shape used in GET /tickets/{id}.
// OwnerUserID is promoted from the embedded Ticket (no shadow field).
type TicketDetail struct {
	Ticket
	EventID     uuid.UUID `json:"event_id"`
	EventName   string    `json:"event_name"`
	SectionName string    `json:"section_name"`
}

// QRResponse is the shape for GET /tickets/{id}/qr.
type QRResponse struct {
	QRData    string    `json:"qr_data"`
	ExpiresAt time.Time `json:"expires_at"`
	RefreshIn int       `json:"refresh_in"`
}

// AssignRequest is the body for PATCH /tickets/{id}/assign.
type AssignRequest struct {
	UserID uuid.UUID `json:"user_id"`
}

// QRPayload is the decoded QR shape used by the verifier (FASE 6 gate).
type QRPayload struct {
	TicketID  uuid.UUID
	Signature string
}
