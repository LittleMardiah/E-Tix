package resale

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
)

type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type ResaleService struct {
	repo    ResaleRepository
	db      TxBeginner
	queue   *queue.Connection      // FIX #13: actual type from publisher.go:10
	gw      adapter.PaymentGateway // P3: BuyResaleTicket payment init (S5-1)
	feeRate string
}

func NewResaleService(repo ResaleRepository, db TxBeginner, q *queue.Connection, gw adapter.PaymentGateway, feeRate string) *ResaleService {
	return &ResaleService{repo: repo, db: db, queue: q, gw: gw, feeRate: feeRate}
}

// Stubs so E1 handler skeleton compiles. E2..E5 replace each one.

func (s *ResaleService) ListTicket(ctx context.Context, sellerID uuid.UUID, req *ListTicketRequest) (*ListTicketResponse, error) {
	t, err := s.repo.TicketInfoForResale(ctx, req.TicketID)
	if err != nil {
		return nil, err
	}
	if t.OwnerUserID != sellerID {
		return nil, ErrResaleForbidden
	}
	if t.Status != "CONFIRMED" {
		return nil, ErrResaleTicketNotConfirmed
	}

	active, err := s.repo.HasActiveListing(ctx, req.TicketID)
	if err != nil {
		return nil, err
	}
	if active {
		return nil, ErrResaleListingExists
	}

	var capPercent int
	if err := s.db.(interface {
		QueryRow(context.Context, string, ...interface{}) pgx.Row
	}).QueryRow(ctx, `SELECT (value::text)::int FROM system_config WHERE key='resale_price_cap_percent'`).Scan(&capPercent); err != nil {
		return nil, err
	}

	origDec, err := parseDecimal(t.SectionPrice)
	if err != nil {
		return nil, err
	}
	resaleDec, err := parseDecimal(req.ResalePrice)
	if err != nil {
		return nil, err
	}
	feeRate, err := parseDecimal(s.feeRate)
	if err != nil {
		return nil, err
	}

	// FIX #15: cap check enforced (was `_ = capPercent` in V1)
	maxPrice := origDec.Mul(decimal.NewFromInt(int64(100 + capPercent)).Div(decimal.NewFromInt(100)))
	if resaleDec.GreaterThan(maxPrice) {
		return nil, ErrResalePriceExceedsCap
	}

	fee := resaleDec.Mul(feeRate)
	payout := resaleDec.Sub(fee)

	now := time.Now()
	rt := &ResaleTransaction{
		ID: uuid.New(), TicketID: req.TicketID, EventID: t.EventID,
		SellerUserID: sellerID, OriginalPrice: t.SectionPrice,
		ResalePrice: req.ResalePrice, PlatformFee: fee.String(),
		SellerPayout: payout.String(), Status: "PENDING", CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repo.Create(ctx, rt); err != nil {
		return nil, err
	}

	return &ListTicketResponse{
		ResaleID: rt.ID, TicketID: rt.TicketID, OriginalPrice: rt.OriginalPrice,
		ResalePrice: rt.ResalePrice, PlatformFee: rt.PlatformFee,
		SellerPayout: rt.SellerPayout, Status: rt.Status, CreatedAt: rt.CreatedAt,
	}, nil
}
func (s *ResaleService) Browse(ctx context.Context, eventID uuid.UUID) (*BrowseResponse, error) {
	rows, err := s.repo.ListPending(ctx, eventID)
	if err != nil {
		return nil, err
	}
	return &BrowseResponse{Listings: rows}, nil
}
func (s *ResaleService) BuyResaleTicket(ctx context.Context, buyerID uuid.UUID, resaleID uuid.UUID) (*BuyResponse, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rt, err := s.repo.GetByIDForUpdate(ctx, tx, resaleID)
	if err != nil {
		return nil, err
	}
	if rt.Status != "PENDING" {
		return nil, ErrResaleNotPending
	}
	if rt.BuyerUserID != nil {
		return nil, ErrResaleAlreadyReserved
	}
	if rt.SellerUserID == buyerID {
		return nil, ErrResaleOwnListing
	}

	if err := s.repo.UpdateBuyer(ctx, tx, resaleID, buyerID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	reservationExpiresAt := time.Now().Add(180 * time.Second)

	// P3 (S5-1): initiate real gateway payment + persist transaction id.
	// Reference namespace "resale:" distinguishes from booking references.
	// Adapter uses BookingID field as the gateway reference slot (see InitiateRequest).
	var paymentLink string
	if s.gw != nil {
		resp, gwErr := s.gw.Initiate(ctx, adapter.InitiateRequest{
			UserID:    buyerID.String(),
			BookingID: "resale:" + resaleID.String(),
			Amount:    rt.ResalePrice,
		})
		if gwErr != nil {
			// Payment init failed: release reservation so buyer may retry cleanly.
			if rtx, e := s.db.Begin(ctx); e == nil {
				_ = s.repo.ClearBuyer(ctx, rtx, resaleID)
				_ = rtx.Commit(ctx)
			}
			return nil, gwErr
		}
		paymentLink = resp.PaymentLink
		if err := s.repo.SetPaymentTransactionID(ctx, resaleID, resp.TransactionID); err != nil {
			return nil, err
		}
	}
	if paymentLink == "" {
		paymentLink = "https://payment.example.com/" + resaleID.String() // fallback stub (gw nil)
	}

	// FIX #12: PublishDelayed(ctx, queueName string, payload []byte, delay time.Duration)
	if s.queue != nil {
		body, _ := json.Marshal(ResaleExpiryPayload{ResaleID: resaleID})
		_ = s.queue.PublishDelayed(ctx, "resale_expiry_check", body, 180*time.Second)
	}

	return &BuyResponse{
		ResaleID: resaleID, Status: "RESERVED",
		ReservationExpiresAt: reservationExpiresAt,
		PaymentLink:          paymentLink,
	}, nil
}
func (s *ResaleService) Cancel(ctx context.Context, sellerID uuid.UUID, resaleID uuid.UUID) (*CancelResponse, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rt, err := s.repo.GetByIDForUpdate(ctx, tx, resaleID)
	if err != nil {
		return nil, err
	}
	if rt.SellerUserID != sellerID {
		return nil, ErrResaleForbidden
	}
	if rt.Status != "PENDING" || rt.BuyerUserID != nil {
		return nil, ErrResaleNotPending
	}

	if err := s.repo.UpdateStatus(ctx, tx, resaleID, "CANCELLED"); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &CancelResponse{ResaleID: resaleID, Status: "CANCELLED", CancelledAt: time.Now()}, nil
}
