package resale

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ResaleTransaction struct {
	ID                   uuid.UUID  `json:"id"`
	TicketID             uuid.UUID  `json:"ticket_id"`
	EventID              uuid.UUID  `json:"event_id"`
	SellerUserID         uuid.UUID  `json:"seller_user_id"`
	BuyerUserID          *uuid.UUID `json:"buyer_user_id,omitempty"`
	OriginalPrice        string     `json:"original_price"`
	ResalePrice          string     `json:"resale_price"`
	PlatformFee          string     `json:"platform_fee"`
	SellerPayout         string     `json:"seller_payout"`
	Status               string     `json:"status"`
	PaymentTransactionID *string    `json:"payment_transaction_id,omitempty"`
	PayoutTransactionID  *string    `json:"payout_transaction_id,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	CompletedAt          *time.Time `json:"completed_at,omitempty"`
	CancelledAt          *time.Time `json:"cancelled_at,omitempty"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// TicketForResale is the ticket snapshot needed by ListTicket (E2).
type TicketForResale struct {
	OwnerUserID  uuid.UUID
	Status       string
	SectionPrice string // decimal as string
	EventID      uuid.UUID
}

type ListTicketRequest struct {
	TicketID    uuid.UUID `json:"ticket_id"    binding:"required"`
	ResalePrice string    `json:"resale_price" binding:"required"`
}

type ListTicketResponse struct {
	ResaleID      uuid.UUID `json:"resale_id"`
	TicketID      uuid.UUID `json:"ticket_id"`
	OriginalPrice string    `json:"original_price"`
	ResalePrice   string    `json:"resale_price"`
	PlatformFee   string    `json:"platform_fee"`
	SellerPayout  string    `json:"seller_payout"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

type BrowseResponse struct {
	Listings []BrowseListing `json:"listings"`
}

type BrowseListing struct {
	ResaleID      uuid.UUID `json:"resale_id"`
	TicketID      uuid.UUID `json:"ticket_id"`
	SellerName    string    `json:"seller_name"`
	Seat          string    `json:"seat"`
	ResalePrice   string    `json:"resale_price"`
	PlatformFee   string    `json:"platform_fee"`
	SellerPayout  string    `json:"seller_payout"`
	OriginalPrice string    `json:"original_price"`
	CreatedAt     time.Time `json:"created_at"`
}

type BuyResponse struct {
	ResaleID             uuid.UUID `json:"resale_id"`
	Status               string    `json:"status"`
	ReservationExpiresAt time.Time `json:"reservation_expires_at"`
	PaymentLink          string    `json:"payment_link"`
}

type CancelResponse struct {
	ResaleID    uuid.UUID `json:"resale_id"`
	Status      string    `json:"status"`
	CancelledAt time.Time `json:"cancelled_at"`
}

type ResaleExpiryPayload struct {
	ResaleID uuid.UUID `json:"resale_id"`
}

type PayoutPayload struct {
	ResaleID     uuid.UUID `json:"resale_id"`
	SellerPayout string    `json:"seller_payout"`
	SellerUserID uuid.UUID `json:"seller_user_id"`
}

func (r *ResaleTransaction) SafeLog() (string, error) {
	b, err := json.Marshal(map[string]interface{}{
		"resale_id": r.ID.String(), "status": r.Status,
	})
	return string(b), err
}
