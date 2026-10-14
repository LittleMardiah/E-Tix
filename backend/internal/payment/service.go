package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
)

// TxBeginner abstracts the transaction opener for testability. In production
// it is *pgxpool.Pool; in unit tests, a fake returning a stub pgx.Tx.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// BookingReader is the minimum booking-side interface needed by the payment
// service. Kept as an interface (not a concrete import) to keep the payment
// package self-contained and test-friendly. Implemented at composition time.
type BookingReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (*BookingSnapshot, error)
	GetByIDForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*BookingSnapshot, error)
}

// BookingSnapshot is the payment-service view of a bookings row.
type BookingSnapshot struct {
	ID            uuid.UUID
	LeaderUserID  uuid.UUID
	TotalAmount   string
	PaymentMode   string
	PaymentStatus string
	ExpiresAt     time.Time
}

// PaymentService orchestrates the payment flow. FASE 4 scope: initiate only.
type PaymentService struct {
	db         TxBeginner
	repo       PaymentObligationRepository
	booking    BookingReader
	gateway    adapter.PaymentGateway
	idempotent *IdempotencyService
}

// NewPaymentService wires the service. booking may be nil for read-only paths.
func NewPaymentService(
	db TxBeginner,
	repo PaymentObligationRepository,
	booking BookingReader,
	gateway adapter.PaymentGateway,
	idem *IdempotencyService,
) *PaymentService {
	return &PaymentService{
		db:         db,
		repo:       repo,
		booking:    booking,
		gateway:    gateway,
		idempotent: idem,
	}
}

// InitiatePayments creates the payment obligations for a booking.
//
// SPLIT: N obligations, one per member, seat_position required.
// BULK:  1 obligation for the leader, seat_position NULL.
//
// Idempotency: L1 (Redis) short-circuits with cached response; L2 (Postgres)
// uses the UNIQUE idempotency_key column. On L1 miss + L2 hit, the cached
// response is reconstructed from the obligation rows.
func (s *PaymentService) InitiatePayments(
	ctx context.Context,
	userID, bookingID uuid.UUID,
	req *InitiatePaymentRequest,
	clientIdempotencyKey string,
) (*InitiatePaymentResponse, error) {
	if clientIdempotencyKey == "" {
		return nil, fmt.Errorf("payment: idempotency key is required")
	}
	if req.PaymentMode != ModeSplit && req.PaymentMode != ModeBulk {
		return nil, ErrInvalidPaymentMode
	}

	hash := ComputeIdempotencyHash(clientIdempotencyKey, userID, bookingID)

	// L1 fast path.
	if body, hit, err := s.idempotent.LookupL1(ctx, hash); err != nil {
		return nil, fmt.Errorf("payment: idempotency L1 lookup: %w", err)
	} else if hit {
		var cached InitiatePaymentResponse
		if err := json.Unmarshal(body, &cached); err != nil {
			// Corrupted cache entry — fall through and rebuild.
			_ = s.idempotent.StoreL1(ctx, hash, []byte{}) // best-effort invalidate
		} else {
			return &cached, nil
		}
	}

	// L2 lookup.
	if existing, err := s.idempotent.LookupL2(ctx, hash); err != nil {
		return nil, fmt.Errorf("payment: idempotency L2 lookup: %w", err)
	} else if existing != nil {
		return s.buildInitiateResponseFromObligation(ctx, existing)
	}

	// Validate payload for the given mode.
	if req.PaymentMode == ModeSplit {
		if len(req.Members) == 0 || len(req.Members) > 10 {
			return nil, ErrInvalidMembers
		}
	} else {
		if req.Amount == "" {
			return nil, ErrInvalidMembers
		}
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("payment: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	bk, err := s.booking.GetByIDForUpdate(ctx, tx, bookingID)
	if err != nil {
		return nil, err
	}
	if bk.LeaderUserID != userID {
		return nil, ErrNotLeader
	}
	if bk.PaymentStatus != StatusPending {
		return nil, ErrBookingNotPending
	}

	var obligations []*PaymentObligation
	if req.PaymentMode == ModeSplit {
		obligations, err = s.initiateSplit(ctx, bk, req, hash)
	} else {
		obligations, err = s.initiateBulk(ctx, bk, req, hash)
	}
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("payment: commit: %w", err)
	}

	resp := s.buildResponse(bookingID, obligations)
	body, _ := json.Marshal(resp)
	if err := s.idempotent.StoreL1(ctx, hash, body); err != nil {
		// Non-fatal: DB has the obligations; L1 will be repopulated on next hit.
		// Log at caller level if needed.
		_ = err
	}
	return resp, nil
}

// initiateSplit creates one obligation per member, with seat_position set.
func (s *PaymentService) initiateSplit(ctx context.Context, bk *BookingSnapshot, req *InitiatePaymentRequest, hash string) ([]*PaymentObligation, error) {
	seen := map[int]bool{}
	out := make([]*PaymentObligation, 0, len(req.Members))

	for i, m := range req.Members {
		if m.SeatPosition <= 0 {
			return nil, fmt.Errorf("payment: seat_position must be > 0")
		}
		if seen[m.SeatPosition] {
			return nil, fmt.Errorf("payment: duplicate seat_position %d", m.SeatPosition)
		}
		seen[m.SeatPosition] = true

		amount, err := decimal.NewFromString(m.Amount)
		if err != nil || amount.LessThanOrEqual(decimal.Zero) {
			return nil, fmt.Errorf("payment: invalid member amount %q", m.Amount)
		}

		gwResp, err := s.gateway.Initiate(ctx, adapter.InitiateRequest{
			UserID:    m.UserID.String(),
			BookingID: bk.ID.String(),
			Amount:    amount.StringFixed(2),
			Method:    MethodVA,
		})
		if err != nil {
			return nil, fmt.Errorf("payment: gateway initiate (member %d): %w", i, err)
		}

		// Idempotency key: only the first obligation carries the client hash,
		// subsequent ones get a suffix so the UNIQUE constraint holds.
		var key *string
		if i == 0 {
			k := hash
			key = &k
		} else {
			k := fmt.Sprintf("%s-%d", hash, i)
			key = &k
		}

		seatPos := m.SeatPosition
		o := &PaymentObligation{
			ID:                    uuid.New(),
			BookingID:             bk.ID,
			UserID:                m.UserID,
			SeatPosition:          &seatPos,
			Amount:                amount.StringFixed(2),
			Status:                StatusPending,
			PaymentLink:           &gwResp.PaymentLink,
			ExternalTransactionID: &gwResp.TransactionID,
			IdempotencyKey:        key,
			CreatedAt:             time.Now().UTC(),
			UpdatedAt:             time.Now().UTC(),
		}
		if err := s.repo.Create(ctx, o); err != nil {
			return nil, fmt.Errorf("payment: insert obligation: %w", err)
		}
		out = append(out, o)
	}

	return out, nil
}

// initiateBulk creates a single obligation (leader pays all, seat_position NULL).
func (s *PaymentService) initiateBulk(ctx context.Context, bk *BookingSnapshot, req *InitiatePaymentRequest, hash string) ([]*PaymentObligation, error) {
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("payment: invalid bulk amount %q", req.Amount)
	}

	gwResp, err := s.gateway.Initiate(ctx, adapter.InitiateRequest{
		UserID:    bk.LeaderUserID.String(),
		BookingID: bk.ID.String(),
		Amount:    amount.StringFixed(2),
		Method:    MethodVA,
	})
	if err != nil {
		return nil, fmt.Errorf("payment: gateway initiate: %w", err)
	}

	k := hash
	o := &PaymentObligation{
		ID:                    uuid.New(),
		BookingID:             bk.ID,
		UserID:                bk.LeaderUserID,
		SeatPosition:          nil,
		Amount:                amount.StringFixed(2),
		Status:                StatusPending,
		PaymentLink:           &gwResp.PaymentLink,
		ExternalTransactionID: &gwResp.TransactionID,
		IdempotencyKey:        &k,
		CreatedAt:             time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	if err := s.repo.Create(ctx, o); err != nil {
		return nil, fmt.Errorf("payment: insert obligation: %w", err)
	}
	return []*PaymentObligation{o}, nil
}

// buildResponse assembles the API response from freshly-created obligations.
func (s *PaymentService) buildResponse(bookingID uuid.UUID, obs []*PaymentObligation) *InitiatePaymentResponse {
	out := make([]ObligationResponse, 0, len(obs))
	for _, o := range obs {
		r := ObligationResponse{
			UserID: o.UserID.String(),
			Amount: o.Amount,
			Status: o.Status,
		}
		if o.SeatPosition != nil {
			sp := *o.SeatPosition
			r.SeatPosition = &sp
		}
		if o.PaymentLink != nil {
			r.PaymentLink = *o.PaymentLink
		}
		if o.ExternalTransactionID != nil {
			r.ExternalTransactionID = *o.ExternalTransactionID
		}
		out = append(out, r)
	}
	return &InitiatePaymentResponse{
		BookingID:     bookingID.String(),
		PaymentStatus: StatusPending,
		Obligations:   out,
		ExpiresAt:     time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339),
	}
}

// buildInitiateResponseFromObligation rebuilds the response on L2 idempotency hit.
func (s *PaymentService) buildInitiateResponseFromObligation(ctx context.Context, first *PaymentObligation) (*InitiatePaymentResponse, error) {
	all, err := s.repo.ListByBooking(ctx, first.BookingID)
	if err != nil {
		return nil, fmt.Errorf("payment: list by booking: %w", err)
	}
	pointers := make([]*PaymentObligation, 0, len(all))
	for i := range all {
		pointers = append(pointers, &all[i])
	}
	return s.buildResponse(first.BookingID, pointers), nil
}
