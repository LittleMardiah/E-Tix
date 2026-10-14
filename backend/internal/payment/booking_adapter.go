package payment

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LittleMardiah/E-Tix/backend/internal/booking"
)

// NewBookingReaderFromRepo wraps a booking.BookingRepository into a
// payment.BookingReader. This adapter exists so the payment domain stays
// free of concrete booking imports and so the composition root can inject
// either a real repository or a fake reader.
//
// Error translation: booking.ErrBookingNotFound is mapped to the local
// payment.ErrBookingNotFound sentinel so callers use errors.Is uniformly.
func NewBookingReaderFromRepo(repo booking.BookingRepository) BookingReader {
	return &bookingReaderAdapter{repo: repo}
}

type bookingReaderAdapter struct {
	repo booking.BookingRepository
}

func (a *bookingReaderAdapter) GetByID(ctx context.Context, id uuid.UUID) (*BookingSnapshot, error) {
	bk, err := a.repo.GetByID(ctx, id)
	if err != nil {
		return nil, translateBookingErr(err)
	}
	return toBookingSnapshot(bk), nil
}

func (a *bookingReaderAdapter) GetByIDForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*BookingSnapshot, error) {
	bk, err := a.repo.GetByIDForUpdate(ctx, tx, id)
	if err != nil {
		return nil, translateBookingErr(err)
	}
	return toBookingSnapshot(bk), nil
}

func translateBookingErr(err error) error {
	if errors.Is(err, booking.ErrBookingNotFound) {
		return ErrBookingNotFound
	}
	return err
}

func toBookingSnapshot(bk *booking.Booking) *BookingSnapshot {
	return &BookingSnapshot{
		ID:            bk.ID,
		LeaderUserID:  bk.LeaderUserID,
		TotalAmount:   bk.TotalAmount,
		PaymentMode:   bk.PaymentMode,
		PaymentStatus: bk.PaymentStatus,
		ExpiresAt:     bk.ExpiresAt,
	}
}
