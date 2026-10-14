package event

import (
	"time"

	"github.com/google/uuid"
)

// Event represents a row from the events table.
type Event struct {
	ID                uuid.UUID
	OrganizerID       uuid.UUID
	OrganizerName     string
	Name              string
	Description       string
	VenueName         string
	VenueAddress      string
	EventDate         string // YYYY-MM-DD (DATE as text)
	EventStartTime    string // HH:MM:SS (TIME as text)
	EventEndTime      string // HH:MM:SS (TIME as text), empty when NULL
	Status            string
	BannerURL         string
	SeatLayoutVersion int
	CreatedAt         time.Time
	UpdatedAt         time.Time
	PublishedAt       *time.Time
}

// Section represents a row from the sections table.
type Section struct {
	ID             uuid.UUID
	EventID        uuid.UUID
	Name           string
	Price          string // numeric(15,2) as text
	TotalSeats     int
	AvailableSeats int
	LayoutVersion  int
}

// ListEventsResponse is the service-level list payload.
type ListEventsResponse struct {
	Events      []EventSummary `json:"events"`
	Total       int            `json:"total"`
	CurrentPage int            `json:"current_page"`
	TotalPages  int            `json:"total_pages"`
	Limit       int            `json:"limit"`
}

// EventSummary is a catalog list entry per event (API CONTRACT 8.1).
type EventSummary struct {
	EventID          string         `json:"event_id"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	VenueName        string         `json:"venue_name"`
	VenueAddress     string         `json:"venue_address"`
	EventDate        string         `json:"event_date"`
	EventStartTime   string         `json:"event_start_time"`
	Status           string         `json:"status"`
	BannerURL        string         `json:"banner_url"`
	MinPrice         string         `json:"min_price"`
	MaxPrice         string         `json:"max_price"`
	TicketsAvailable int            `json:"tickets_available"`
	TotalTickets     int            `json:"total_tickets"`
	Sections         []SectionBrief `json:"sections"`
}

// SectionBrief is the compact section info inside EventSummary.
type SectionBrief struct {
	SectionID string `json:"section_id"`
	Name      string `json:"name"`
	Price     string `json:"price"`
}

// EventDetailResponse is the detail payload (API CONTRACT 8.2).
type EventDetailResponse struct {
	EventID           string          `json:"event_id"`
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	VenueName         string          `json:"venue_name"`
	VenueAddress      string          `json:"venue_address"`
	EventDate         string          `json:"event_date"`
	EventStartTime    string          `json:"event_start_time"`
	EventEndTime      string          `json:"event_end_time"`
	Status            string          `json:"status"`
	BannerURL         string          `json:"banner_url"`
	Organizer         OrganizerBrief  `json:"organizer"`
	Sections          []SectionDetail `json:"sections"`
	SeatLayoutVersion int             `json:"seat_layout_version"`
	CreatedAt         string          `json:"created_at"`
}

// OrganizerBrief identifies the event organizer (stored in users table).
type OrganizerBrief struct {
	OrganizerID string `json:"organizer_id"`
	Name        string `json:"name"`
}

// SectionDetail is a section entry inside EventDetailResponse.
type SectionDetail struct {
	SectionID      string `json:"section_id"`
	Name           string `json:"name"`
	Price          string `json:"price"`
	TotalSeats     int    `json:"total_seats"`
	AvailableSeats int    `json:"available_seats"`
}
