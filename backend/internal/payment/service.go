package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	"github.com/LittleMardiah/E-Tix/backend/internal/resale"
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
	GetEventStartAt(ctx context.Context, bookingID uuid.UUID) (time.Time, error)
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

// P3b: minimal resale surface used by webhook — avoids import cycle.
// ResaleTxLite is a type alias to the real row so the Postgres repo satisfies
// this interface without an extra adapter (resale does not import payment root).
type ResaleWebhookLookup interface {
	GetByPaymentTransactionID(ctx context.Context, txID string) (*ResaleTxLite, error)
	UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error
}

type ResaleTxLite = resale.ResaleTransaction

// PaymentService orchestrates the payment flow. FASE 4 scope: initiate only.
type PaymentService struct {
	db         TxBeginner
	repo       PaymentObligationRepository
	booking    BookingReader
	gateway    adapter.PaymentGateway
	idempotent *IdempotencyService
	resaleRepo ResaleWebhookLookup // P3b: optional resale branch
	mq         queue.Publisher     // P3b: for PayoutPayload enqueue (interface: testable without live MQ)
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

// SetResaleWebhookDeps wires resale repo + mq for the resale webhook branch.
// Setter pattern (not constructor param) to avoid breaking FASE 4 call sites.
func (s *PaymentService) SetResaleWebhookDeps(repo ResaleWebhookLookup, mq queue.Publisher) {
	s.resaleRepo = repo
	s.mq = mq
}

const paymentDBTimeout = 5 * time.Second

// txObligationCreator is implemented by repositories that can run the insert
// on the caller's transaction. It is required when the booking row is locked
// FOR UPDATE in that transaction: inserting the child row from a separate
// pool connection makes PostgreSQL wait for a KEY SHARE lock on the parent,
// which cannot be granted until the caller commits. That application-level
// deadlock ends as SQLSTATE 57014 or context canceled.
type txObligationCreator interface {
	CreateTx(ctx context.Context, tx pgx.Tx, o *PaymentObligation) error
}

func (s *PaymentService) createObligation(ctx context.Context, tx pgx.Tx, o *PaymentObligation) error {
	if creator, ok := s.repo.(txObligationCreator); ok {
		return creator.CreateTx(ctx, tx, o)
	}
	return s.repo.Create(ctx, o)
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

	// Read booking without long lock if possible; still need to validate.
	bk, err := s.booking.GetByID(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	if bk.LeaderUserID != userID {
		return nil, ErrNotLeader
	}
	if bk.PaymentStatus != StatusPending {
		return nil, ErrBookingNotPending
	}

	// Bound the DB phase so a blocked lock cannot pin the request until the
	// client disconnects. The gateway runs after commit with the request ctx.
	dbCtx, cancelDB := context.WithTimeout(ctx, paymentDBTimeout)
	defer cancelDB()

	// Create obligations in the same transaction that holds the booking lock.
	// A pool-backed insert would wait for a FK KEY SHARE lock on the locked
	// booking row until statement_timeout (SQLSTATE 57014).
	tx, err := s.db.Begin(dbCtx)
	if err != nil {
		return nil, fmt.Errorf("payment: begin tx: %w", err)
	}
	defer func() {
		// Release the row lock even when the request context was canceled;
		// otherwise the transaction can stay idle-in-transaction and block
		// the next request.
		rbCtx, rbCancel := context.WithTimeout(context.Background(), paymentDBTimeout)
		defer rbCancel()
		_ = tx.Rollback(rbCtx)
	}()

	// Re-check under lock to prevent race conditions.
	bkLocked, err := s.booking.GetByIDForUpdate(dbCtx, tx, bookingID)
	if err != nil {
		return nil, err
	}
	if bkLocked.LeaderUserID != userID {
		return nil, ErrNotLeader
	}
	if bkLocked.PaymentStatus != StatusPending {
		return nil, ErrBookingNotPending
	}
	bk = bkLocked

	var obligations []*PaymentObligation
	if req.PaymentMode == ModeSplit {
		obligations, err = s.initiateSplit(dbCtx, tx, bk, req, hash)
	} else {
		obligations, err = s.initiateBulk(dbCtx, tx, bk, req, hash)
	}
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(dbCtx); err != nil {
		return nil, fmt.Errorf("payment: commit: %w", err)
	}

	// Call gateway AFTER transaction is committed to avoid holding locks
	// during external/network calls.
	if req.PaymentMode == ModeSplit {
		if err := s.finalizeSplitWithGateway(ctx, bk, req, obligations); err != nil {
			return nil, err
		}
	} else {
		if err := s.finalizeBulkWithGateway(ctx, bk, req, obligations); err != nil {
			return nil, err
		}
	}

	resp := s.buildResponse(bookingID, obligations)
	body, _ := json.Marshal(resp)
	if err := s.idempotent.StoreL1(ctx, hash, body); err != nil {
		// Non-fatal: DB has the obligations; L1 will be repopulated on next hit.
		_ = err
	}
	return resp, nil
}

// initiateSplit creates one obligation per member, with seat_position set.
// gatewayResp removed (unused)

func (s *PaymentService) initiateSplit(ctx context.Context, tx pgx.Tx, bk *BookingSnapshot, req *InitiatePaymentRequest, hash string) ([]*PaymentObligation, error) {
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
			ID:             uuid.New(),
			BookingID:      bk.ID,
			UserID:         m.UserID,
			SeatPosition:   &seatPos,
			Amount:         amount.StringFixed(2),
			Status:         StatusPending,
			IdempotencyKey: key,
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		}
		if err := s.createObligation(ctx, tx, o); err != nil {
			return nil, fmt.Errorf("payment: insert obligation: %w", err)
		}
		out = append(out, o)
	}

	return out, nil
}

func (s *PaymentService) finalizeSplitWithGateway(ctx context.Context, bk *BookingSnapshot, req *InitiatePaymentRequest, obligations []*PaymentObligation) error {
	for i, m := range req.Members {
		if i >= len(obligations) {
			break
		}
		amount, _ := decimal.NewFromString(m.Amount)
		gwResp, err := s.gateway.Initiate(ctx, adapter.InitiateRequest{
			UserID:    m.UserID.String(),
			BookingID: bk.ID.String(),
			Amount:    amount.StringFixed(2),
			Method:    MethodVA,
		})
		if err != nil {
			return fmt.Errorf("payment: gateway initiate (member %d): %w", i, err)
		}
		obligations[i].PaymentLink = &gwResp.PaymentLink
		obligations[i].ExternalTransactionID = &gwResp.TransactionID
		obligations[i].UpdatedAt = time.Now().UTC()
		if err := s.repo.UpdateGatewayInfo(ctx, obligations[i].ID, gwResp.PaymentLink, gwResp.TransactionID); err != nil {
			return fmt.Errorf("payment: update obligation gateway: %w", err)
		}
	}
	return nil
}

// initiateBulk creates a single obligation (leader pays all, seat_position NULL).
func (s *PaymentService) initiateBulk(ctx context.Context, tx pgx.Tx, bk *BookingSnapshot, req *InitiatePaymentRequest, hash string) ([]*PaymentObligation, error) {
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("payment: invalid bulk amount %q", req.Amount)
	}

	k := hash
	o := &PaymentObligation{
		ID:             uuid.New(),
		BookingID:      bk.ID,
		UserID:         bk.LeaderUserID,
		SeatPosition:   nil,
		Amount:         amount.StringFixed(2),
		Status:         StatusPending,
		IdempotencyKey: &k,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := s.createObligation(ctx, tx, o); err != nil {
		return nil, fmt.Errorf("payment: insert obligation: %w", err)
	}
	return []*PaymentObligation{o}, nil
}

func (s *PaymentService) finalizeBulkWithGateway(ctx context.Context, bk *BookingSnapshot, req *InitiatePaymentRequest, obligations []*PaymentObligation) error {
	if len(obligations) == 0 {
		return nil
	}
	amount, _ := decimal.NewFromString(req.Amount)
	gwResp, err := s.gateway.Initiate(ctx, adapter.InitiateRequest{
		UserID:    bk.LeaderUserID.String(),
		BookingID: bk.ID.String(),
		Amount:    amount.StringFixed(2),
		Method:    MethodVA,
	})
	if err != nil {
		return fmt.Errorf("payment: gateway initiate: %w", err)
	}
	obligations[0].PaymentLink = &gwResp.PaymentLink
	obligations[0].ExternalTransactionID = &gwResp.TransactionID
	obligations[0].UpdatedAt = time.Now().UTC()
	if err := s.repo.UpdateGatewayInfo(ctx, obligations[0].ID, gwResp.PaymentLink, gwResp.TransactionID); err != nil {
		return fmt.Errorf("payment: update obligation gateway: %w", err)
	}
	return nil
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

// HandleWebhook processes an inbound payment gateway webhook.
//
// Per ROADMAP FASE 4 §3.5 + FLAG #7:
//   - Signature validated via adapter (header primary, body fallback).
//   - PENDING / CHALLENGE → ACK without DB mutation (transitional).
//   - SUCCESS / SETTLEMENT / CAPTURE → PAID.
//   - DENY / CANCEL / EXPIRE / FAILURE → FAILED.
//   - Duplicate PAID webhook → no-op, returns nil.
//
// Callers return HTTP 200 when err == nil, even for transitional statuses
// (prevents retry storms).
func (s *PaymentService) HandleWebhook(ctx context.Context, payload []byte, headers map[string]string) error {
	if err := s.gateway.ValidateWebhookSignature(headers, payload); err != nil {
		return fmt.Errorf("%w: %v", ErrWebhookInvalidSig, err)
	}

	var p WebhookPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("payment: unmarshal webhook: %w", err)
	}
	if p.ExternalTransactionID == "" {
		return fmt.Errorf("payment: webhook missing external_transaction_id")
	}

	newStatus := classifyWebhookStatus(p.Status)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("payment: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	obligation, err := s.repo.GetByExternalTxIDForUpdate(ctx, tx, p.ExternalTransactionID)

	if err != nil {
		// P3b: try resale branch before failing (obligation may not exist for resale payments)
		if s.resaleRepo != nil {
			if rt, rerr := s.resaleRepo.GetByPaymentTransactionID(ctx, p.ExternalTransactionID); rerr == nil && rt != nil {
				return s.handleResaleWebhook(ctx, rt, newStatus)
			}
		}
		return err
	}

	// Transitional (PENDING/CHALLENGE) → ACK, no mutation.
	if newStatus == "" {
		return nil
	}
	// Idempotent: already PAID and inbound is PAID → no-op.
	if obligation.Status == StatusPaid && newStatus == StatusPaid {
		return nil
	}
	// Already in final state, inbound would downgrade → ignore.
	if obligation.Status == StatusPaid && newStatus == StatusFailed {
		return nil
	}

	if err := s.repo.UpdateStatusWithWebhook(ctx, tx, obligation.ID, newStatus); err != nil {
		return fmt.Errorf("payment: update status: %w", err)
	}
	return tx.Commit(ctx)
}

// classifyWebhookStatus maps a provider status string to an internal
// payment_status_enum value. Returns "" for transitional states that
// should NOT be persisted.
func classifyWebhookStatus(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "SUCCESS", "SETTLEMENT", "CAPTURE":
		return StatusPaid
	case "DENY", "CANCEL", "EXPIRE", "FAILURE":
		return StatusFailed
	default:
		// PENDING, CHALLENGE, unknown — do not mutate DB.
		return ""
	}
}

// BulkAssign assigns a batch of tickets from the same booking to target users.
//
// Per ROADMAP FASE 4 §3.5:
//   - Caller must be the booking leader.
//   - Event must not have started yet (422 EVENT_ALREADY_STARTED).
//   - Each target user must be ACTIVE (422 TARGET_USER_INACTIVE_OR_SUSPENDED).
//   - Atomic all-or-nothing: any failure rolls back the whole batch.
func (s *PaymentService) BulkAssign(ctx context.Context, userID uuid.UUID, req *BulkAssignRequest) (*BulkAssignResponse, error) {
	if req.BookingID == uuid.Nil || len(req.Assignments) == 0 {
		return nil, ErrInvalidMembers
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("payment: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	bk, err := s.booking.GetByIDForUpdate(ctx, tx, req.BookingID)
	if err != nil {
		return nil, err
	}
	if bk.LeaderUserID != userID {
		return nil, ErrNotLeader
	}

	startAt, err := s.booking.GetEventStartAt(ctx, req.BookingID)
	if err != nil {
		return nil, err
	}
	if !startAt.After(time.Now().UTC()) {
		return nil, ErrEventAlreadyStarted
	}

	// Deterministic lock order (deadlock prevention): sort assignments by
	// TicketID so concurrent BulkAssign calls for the same booking acquire
	// the per-user FOR UPDATE locks in a consistent order.
	assignments := make([]BulkAssignEntry, len(req.Assignments))
	copy(assignments, req.Assignments)
	sort.Slice(assignments, func(i, j int) bool {
		return assignments[i].TicketID.String() < assignments[j].TicketID.String()
	})

	results := make([]BulkAssignTicketResult, 0, len(assignments))
	for _, a := range assignments {
		active, err := s.repo.CheckUserActiveForUpdate(ctx, tx, a.UserID)
		if err != nil {
			return nil, fmt.Errorf("payment: check user active: %w", err)
		}
		if !active {
			return nil, ErrTargetUserInactive
		}

		n, err := s.repo.AssignTicketAtomic(ctx, tx, a.TicketID, req.BookingID, a.UserID)
		if err != nil {
			return nil, fmt.Errorf("payment: assign ticket: %w", err)
		}
		if n == 0 {
			return nil, ErrTicketAlreadyAssigned
		}

		results = append(results, BulkAssignTicketResult{
			TicketID:   a.TicketID.String(),
			AssignedTo: a.UserID.String(),
			Status:     "SUCCESS",
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("payment: commit: %w", err)
	}

	return &BulkAssignResponse{
		BookingID: req.BookingID.String(),
		Assigned:  len(results),
		Tickets:   results,
	}, nil
}

// handleResaleWebhook processes a webhook that matches a resale payment.
// newStatus comes from classifyWebhookStatus ("" = transitional, no mutation).
func (s *PaymentService) handleResaleWebhook(ctx context.Context, rt *ResaleTxLite, newStatus string) error {
	if newStatus == "" {
		return nil // transitional — ACK without mutation
	}
	if rt.Status != "PENDING" {
		return nil // already terminal
	}
	if newStatus == StatusPaid {
		tx, err := s.db.Begin(ctx)
		if err != nil {
			return err
		}
		if err := s.resaleRepo.UpdateStatus(ctx, tx, rt.ID, "COMPLETED"); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}

		// Enqueue payout job (worker consumes on P4)
		if s.mq != nil {
			body, _ := json.Marshal(map[string]interface{}{
				"resale_id":      rt.ID.String(),
				"seller_payout":  rt.SellerPayout,
				"seller_user_id": rt.SellerUserID.String(),
			})
			_ = s.mq.PublishToQueue(ctx, "resale_payout", body)
		}
	}
	// Other statuses (FAILED/etc.): leave as PENDING — expiry worker handles cleanup
	return nil
}
