package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrEventNotFound is returned when the requested event does not exist.
var ErrEventNotFound = errors.New("event not found")

// ErrInvalidEventInput wraps validation failures for event creation.
var ErrInvalidEventInput = errors.New("invalid event input")

// ErrEventNotDraft is returned when publishing an event that is not DRAFT.
var ErrEventNotDraft = errors.New("event is not in DRAFT status")

func invalidInput(msg string) error {
	return fmt.Errorf("%w: %s", ErrInvalidEventInput, msg)
}

// CreateEventRequest is the body for POST /eo/events.
type CreateEventRequest struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	VenueName      string `json:"venue_name"`
	VenueAddress   string `json:"venue_address"`
	EventDate      string `json:"event_date"`
	EventStartTime string `json:"event_start_time"`
	EventEndTime   string `json:"event_end_time"`
	SeatingMode    string `json:"seating_mode"`
}

// EventService aggregates catalog reads over EventRepository.
type EventService struct {
	repo EventRepository
}

func NewEventService(repo EventRepository) *EventService {
	return &EventService{repo: repo}
}

func (s *EventService) List(ctx context.Context, filter ListFilter) (*ListEventsResponse, error) {
	events, total, err := s.repo.ListEvents(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	// H5 FIX: batch section fetch instead of N+1. One query for the whole
	// page regardless of event count.
	summaries := make([]EventSummary, 0, len(events))
	if len(events) > 0 {
		ids := make([]uuid.UUID, 0, len(events))
		for _, ev := range events {
			ids = append(ids, ev.ID)
		}
		sectionsByEvent, err := s.repo.GetSectionsByEventIDs(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("get sections batch: %w", err)
		}
		for _, ev := range events {
			summaries = append(summaries, buildEventSummary(ev, sectionsByEvent[ev.ID]))
		}
	}

	totalPages := 0
	if filter.Limit > 0 {
		totalPages = (total + filter.Limit - 1) / filter.Limit
	}
	return &ListEventsResponse{
		Events:      summaries,
		Total:       total,
		CurrentPage: filter.Page,
		TotalPages:  totalPages,
		Limit:       filter.Limit,
	}, nil
}

func (s *EventService) GetByID(ctx context.Context, id uuid.UUID) (*EventDetailResponse, error) {
	ev, err := s.repo.GetEventByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("get event: %w", err)
	}

	sections, err := s.repo.GetSectionsByEventID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get sections: %w", err)
	}

	details := make([]SectionDetail, 0, len(sections))
	for _, sec := range sections {
		details = append(details, SectionDetail{
			SectionID:      sec.ID.String(),
			Name:           sec.Name,
			Price:          formatDecimal(sec.Price),
			TotalSeats:     sec.TotalSeats,
			AvailableSeats: sec.AvailableSeats,
		})
	}

	var layoutImageURL string
	if ev.LayoutImageURL != nil {
		layoutImageURL = *ev.LayoutImageURL
	}
	var layoutZones json.RawMessage
	if len(ev.LayoutZones) > 0 {
		layoutZones = json.RawMessage(ev.LayoutZones)
	}

	resp := &EventDetailResponse{
		EventID:           ev.ID.String(),
		Name:              ev.Name,
		Description:       ev.Description,
		VenueName:         ev.VenueName,
		VenueAddress:      ev.VenueAddress,
		EventDate:         ev.EventDate,
		EventStartTime:    formatHHMM(ev.EventStartTime),
		EventEndTime:      formatHHMM(ev.EventEndTime),
		Status:            ev.Status,
		BannerURL:         ev.BannerURL,
		LayoutImageURL:    layoutImageURL,
		LayoutZones:       layoutZones,
		SeatingMode:       ev.SeatingMode,
		LayoutImageWidth:  ev.LayoutImageWidth,
		LayoutImageHeight: ev.LayoutImageHeight,
		Sections:          details,
		SeatLayoutVersion: ev.SeatLayoutVersion,
		CreatedAt:         ev.CreatedAt.UTC().Format(time.RFC3339),
	}
	resp.Organizer.OrganizerID = ev.OrganizerID.String()
	resp.Organizer.Name = ev.OrganizerName
	return resp, nil
}

type UpdateLayoutRequest struct {
	LayoutImageURL    string `json:"layout_image_url"`
	LayoutImageWidth  int    `json:"layout_image_width"`
	LayoutImageHeight int    `json:"layout_image_height"`
	SeatingMode       string `json:"seating_mode"`
	LayoutZones       string `json:"layout_zones"`
}

// Create inserts a DRAFT event owned by the caller and returns its detail.
func (s *EventService) Create(ctx context.Context, organizerID uuid.UUID, req CreateEventRequest) (*EventDetailResponse, error) {
	name := strings.TrimSpace(req.Name)
	venueName := strings.TrimSpace(req.VenueName)
	venueAddress := strings.TrimSpace(req.VenueAddress)
	if name == "" {
		return nil, invalidInput("name is required")
	}
	if venueName == "" {
		return nil, invalidInput("venue_name is required")
	}
	if venueAddress == "" {
		return nil, invalidInput("venue_address is required")
	}

	eventDate, err := time.Parse("2006-01-02", strings.TrimSpace(req.EventDate))
	if err != nil {
		return nil, invalidInput("event_date must be YYYY-MM-DD")
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if eventDate.Before(today) {
		return nil, invalidInput("event_date must not be in the past")
	}

	startTime, err := time.Parse("15:04", strings.TrimSpace(req.EventStartTime))
	if err != nil {
		return nil, invalidInput("event_start_time must be HH:MM")
	}
	endTime := ""
	if strings.TrimSpace(req.EventEndTime) != "" {
		parsedEnd, err := time.Parse("15:04", strings.TrimSpace(req.EventEndTime))
		if err != nil {
			return nil, invalidInput("event_end_time must be HH:MM")
		}
		if !parsedEnd.After(startTime) {
			return nil, invalidInput("event_end_time must be after event_start_time")
		}
		endTime = parsedEnd.Format("15:04:05")
	}

	mode := strings.ToUpper(strings.TrimSpace(req.SeatingMode))
	if mode == "" {
		mode = "ZONED"
	}
	if mode != "ZONED" && mode != "ASSIGNED" {
		return nil, invalidInput("seating_mode must be ZONED or ASSIGNED")
	}

	ev := &Event{
		OrganizerID:    organizerID,
		Name:           name,
		Description:    strings.TrimSpace(req.Description),
		VenueName:      venueName,
		VenueAddress:   venueAddress,
		EventDate:      eventDate.Format("2006-01-02"),
		EventStartTime: startTime.Format("15:04:05"),
		EventEndTime:   endTime,
		Status:         "DRAFT",
		SeatingMode:    mode,
	}
	if err := s.repo.InsertEvent(ctx, ev); err != nil {
		return nil, fmt.Errorf("create event: %w", err)
	}
	return s.GetByID(ctx, ev.ID)
}

// GetOrganizerID returns the owner of an event.
func (s *EventService) GetOrganizerID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	organizerID, err := s.repo.GetOrganizerID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrEventNotFound
		}
		return uuid.Nil, fmt.Errorf("get organizer id: %w", err)
	}
	return organizerID, nil
}

// Publish flips a DRAFT event to ON_SALE and returns the refreshed detail.
func (s *EventService) Publish(ctx context.Context, id uuid.UUID) (*EventDetailResponse, error) {
	if err := s.repo.PublishDraft(ctx, id); err != nil {
		if errors.Is(err, ErrEventNotDraft) {
			return nil, ErrEventNotDraft
		}
		return nil, fmt.Errorf("publish event: %w", err)
	}
	return s.GetByID(ctx, id)
}

// UpdateLayoutImageURL persists an uploaded map URL only (width/height and
// zones are left untouched until the EO saves the editor).
func (s *EventService) UpdateLayoutImageURL(ctx context.Context, id uuid.UUID, layoutImageURL string) error {
	return s.repo.UpdateLayoutImageURL(ctx, id, layoutImageURL)
}

func (s *EventService) UpdateLayout(ctx context.Context, id uuid.UUID, req UpdateLayoutRequest) error {
	zones := []byte(req.LayoutZones)
	if req.LayoutZones == "" {
		zones = nil
	}
	return s.repo.UpdateEventLayout(ctx, id, req.LayoutImageURL, req.LayoutImageWidth, req.LayoutImageHeight, req.SeatingMode, zones)
}
func buildEventSummary(ev Event, sections []Section) EventSummary {
	briefs := make([]SectionBrief, 0, len(sections))
	totalTickets := 0
	ticketsAvailable := 0
	var minRaw, maxRaw string
	minVal := 0.0
	maxVal := 0.0
	for i, sec := range sections {
		briefs = append(briefs, SectionBrief{
			SectionID: sec.ID.String(),
			Name:      sec.Name,
			Price:     formatDecimal(sec.Price),
		})
		totalTickets += sec.TotalSeats
		ticketsAvailable += sec.AvailableSeats
		if p, err := strconv.ParseFloat(sec.Price, 64); err == nil {
			if i == 0 || p < minVal {
				minVal = p
				minRaw = sec.Price
			}
			if i == 0 || p > maxVal {
				maxVal = p
				maxRaw = sec.Price
			}
		}
	}
	return EventSummary{
		EventID:          ev.ID.String(),
		Name:             ev.Name,
		Description:      ev.Description,
		VenueName:        ev.VenueName,
		VenueAddress:     ev.VenueAddress,
		EventDate:        ev.EventDate,
		EventStartTime:   formatHHMM(ev.EventStartTime),
		Status:           ev.Status,
		BannerURL:        ev.BannerURL,
		MinPrice:         formatDecimal(minRaw),
		MaxPrice:         formatDecimal(maxRaw),
		TicketsAvailable: ticketsAvailable,
		TotalTickets:     totalTickets,
		Sections:         briefs,
	}
}

// formatDecimal strips the trailing zeros of a numeric(15,2) text value
// (e.g. "2000000.00" -> "2000000").
func formatDecimal(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	return s
}

// formatHHMM shortens "HH:MM:SS" to "HH:MM"; empty stays empty.
func formatHHMM(s string) string {
	if s == "" {
		return ""
	}
	if len(s) >= 5 {
		return s[:5]
	}
	return s
}
