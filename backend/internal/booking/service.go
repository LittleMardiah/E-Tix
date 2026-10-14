package booking

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

type BookingService struct {
	repo    BookingRepository
	rdb     *redis.Client
	scripts *LuaScripts
}

func NewBookingService(repo BookingRepository, rdb *redis.Client, scripts *LuaScripts) *BookingService {
	return &BookingService{repo: repo, rdb: rdb, scripts: scripts}
}

func (s *BookingService) GetSeatAvailability(ctx context.Context, eventID, sectionID uuid.UUID) (*SeatAvailabilityResponse, error) {
	sec, err := s.repo.GetSectionByID(ctx, sectionID)
	if err != nil {
		return nil, err
	}
	if sec.EventID != eventID {
		return nil, fmt.Errorf("booking: section %s does not belong to event %s", sectionID, eventID)
	}

	taken, err := s.repo.GetTakenSeats(ctx, sectionID)
	if err != nil {
		return nil, err
	}
	takenMap := make(map[int]struct{}, len(taken))
	for _, p := range taken {
		takenMap[p] = struct{}{}
	}

	seats := make([]SeatStatus, 0, sec.TotalSeats)
	available := 0
	for pos := 1; pos <= sec.TotalSeats; pos++ {
		status := "AVAILABLE"
		if _, ok := takenMap[pos]; ok {
			status = "TAKEN"
		} else {
			available++
		}
		seats = append(seats, SeatStatus{Position: pos, Status: status})
	}

	return &SeatAvailabilityResponse{
		EventID:        eventID.String(),
		SectionID:      sectionID.String(),
		SectionName:    sec.Name,
		LayoutVersion:  sec.LayoutVersion,
		TotalSeats:     sec.TotalSeats,
		AvailableSeats: available,
		Seats:          seats,
		LastUpdatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}
