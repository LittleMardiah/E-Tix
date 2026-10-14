package booking

import (
	"time"

	"github.com/google/uuid"
)

type Booking struct {
	ID            uuid.UUID `json:"id"`
	EventID       uuid.UUID `json:"event_id"`
	SectionID     uuid.UUID `json:"section_id"`
	LeaderUserID  uuid.UUID `json:"leader_user_id"`
	SeatPositions []int     `json:"seat_positions"`
	TotalSeats    int       `json:"total_seats"`
	TotalAmount   string    `json:"total_amount"`
	PaymentMode   string    `json:"payment_mode"`
	PaymentStatus string    `json:"payment_status"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type SeatStatus struct {
	Position int    `json:"position"`
	Status   string `json:"status"` // AVAILABLE | TAKEN | LOCKED
}

type SeatAvailabilityResponse struct {
	EventID        string       `json:"event_id"`
	SectionID      string       `json:"section_id"`
	SectionName    string       `json:"section_name"`
	LayoutVersion  int          `json:"layout_version"`
	TotalSeats     int          `json:"total_seats"`
	AvailableSeats int          `json:"available_seats"`
	Seats          []SeatStatus `json:"seats"`
	LastUpdatedAt  string       `json:"last_updated_at"`
}

type LockSeatsRequest struct {
	SeatPositions []int  `json:"seat_positions"`
	PaymentMode   string `json:"payment_mode"`
}

type LockSeatsResponse struct {
	BookingID     string `json:"booking_id"`
	EventID       string `json:"event_id"`
	SectionID     string `json:"section_id"`
	SeatPositions []int  `json:"seat_positions"`
	TotalSeats    int    `json:"total_seats"`
	TotalAmount   string `json:"total_amount"`
	PaymentMode   string `json:"payment_mode"`
	PaymentStatus string `json:"payment_status"`
	ExpiresAt     string `json:"expires_at"`
}
