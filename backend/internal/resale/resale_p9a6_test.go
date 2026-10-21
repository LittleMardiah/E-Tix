package resale

// STP traceability: UT-RESALE-001..003 — all three mapped below.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
	redisclient "github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// ============================================================
// P9a-6 fakes
// ============================================================

// p9a6ResaleTx is a pgx.Tx stub that only implements Commit/Rollback.
type p9a6ResaleTx struct {
	pgx.Tx
	committed  bool
	rolledBack bool
	commitErr  error
}

func (t *p9a6ResaleTx) Commit(context.Context) error {
	if t.commitErr != nil {
		return t.commitErr
	}
	t.committed = true
	return nil
}

func (t *p9a6ResaleTx) Rollback(context.Context) error {
	t.rolledBack = true
	return nil
}

// p9a6ResaleRow is a pgx.Row stub used for the system_config cap lookup.
type p9a6ResaleRow struct {
	capPercent int
	err        error
}

func (r *p9a6ResaleRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for _, d := range dest {
		switch v := d.(type) {
		case *int:
			*v = r.capPercent
		case *string:
			*v = "10"
		}
	}
	return nil
}

// p9a6ResaleDB implements both TxBeginner and the QueryRow extension the
// service uses to read resale_price_cap_percent.
type p9a6ResaleDB struct {
	tx       pgx.Tx
	beginErr error
	row      *p9a6ResaleRow
	beginN   int
}

func (d *p9a6ResaleDB) Begin(context.Context) (pgx.Tx, error) {
	d.beginN++
	if d.beginErr != nil {
		return nil, d.beginErr
	}
	if d.tx != nil {
		return d.tx, nil
	}
	return &p9a6ResaleTx{}, nil
}

func (d *p9a6ResaleDB) QueryRow(_ context.Context, _ string, _ ...interface{}) pgx.Row {
	if d.row != nil {
		return d.row
	}
	return &p9a6ResaleRow{capPercent: 50}
}

// p9a6ResaleRepo is a configurable ResaleRepository fake.
type p9a6ResaleRepo struct {
	ticket          *TicketForResale
	ticketErr       error
	hasActive       bool
	hasActiveErr    error
	createErr       error
	created         *ResaleTransaction
	listings        []BrowseListing
	listErr         error
	byID            *ResaleTransaction
	byIDErr         error
	updateBuyerErr  error
	buyerSet        []uuid.UUID
	clearBuyerErr   error
	clearBuyerCalls int
	updateStatusErr error
	statusUpdates   []string
	setPaymentErr   error
	paymentTxIDs    []string
}

func (r *p9a6ResaleRepo) Create(_ context.Context, rt *ResaleTransaction) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.created = rt
	return nil
}

func (r *p9a6ResaleRepo) GetByID(context.Context, uuid.UUID) (*ResaleTransaction, error) {
	if r.byIDErr != nil {
		return nil, r.byIDErr
	}
	return r.byID, nil
}

func (r *p9a6ResaleRepo) GetByIDForUpdate(context.Context, pgx.Tx, uuid.UUID) (*ResaleTransaction, error) {
	if r.byIDErr != nil {
		return nil, r.byIDErr
	}
	return r.byID, nil
}

func (r *p9a6ResaleRepo) ListPending(context.Context, uuid.UUID) ([]BrowseListing, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.listings, nil
}

func (r *p9a6ResaleRepo) UpdateBuyer(_ context.Context, _ pgx.Tx, _ uuid.UUID, buyerID uuid.UUID) error {
	if r.updateBuyerErr != nil {
		return r.updateBuyerErr
	}
	r.buyerSet = append(r.buyerSet, buyerID)
	return nil
}

func (r *p9a6ResaleRepo) ClearBuyer(_ context.Context, _ pgx.Tx, _ uuid.UUID) error {
	r.clearBuyerCalls++
	return r.clearBuyerErr
}

func (r *p9a6ResaleRepo) UpdateStatus(_ context.Context, _ pgx.Tx, _ uuid.UUID, status string) error {
	if r.updateStatusErr != nil {
		return r.updateStatusErr
	}
	r.statusUpdates = append(r.statusUpdates, status)
	return nil
}

func (r *p9a6ResaleRepo) HasActiveListing(context.Context, uuid.UUID) (bool, error) {
	if r.hasActiveErr != nil {
		return false, r.hasActiveErr
	}
	return r.hasActive, nil
}

func (r *p9a6ResaleRepo) SetPaymentTransactionID(_ context.Context, _ uuid.UUID, txID string) error {
	if r.setPaymentErr != nil {
		return r.setPaymentErr
	}
	r.paymentTxIDs = append(r.paymentTxIDs, txID)
	return nil
}

func (r *p9a6ResaleRepo) GetByPaymentTransactionID(context.Context, string) (*ResaleTransaction, error) {
	return r.byID, r.byIDErr
}

func (r *p9a6ResaleRepo) TicketInfoForResale(context.Context, uuid.UUID) (*TicketForResale, error) {
	if r.ticketErr != nil {
		return nil, r.ticketErr
	}
	return r.ticket, nil
}

// p9a6ResaleGateway is an adapter.PaymentGateway fake.
type p9a6ResaleGateway struct {
	initiateResp *adapter.InitiateResponse
	initiateErr  error
	refundResp   *adapter.RefundResponse
	refundErr    error
	payoutResp   *adapter.PayoutResponse
	payoutErr    error
	statusResp   *adapter.StatusResponse
	statusErr    error
	sigErr       error
	initiated    []adapter.InitiateRequest
}

func (g *p9a6ResaleGateway) Initiate(_ context.Context, req adapter.InitiateRequest) (*adapter.InitiateResponse, error) {
	g.initiated = append(g.initiated, req)
	if g.initiateErr != nil {
		return nil, g.initiateErr
	}
	if g.initiateResp != nil {
		return g.initiateResp, nil
	}
	return &adapter.InitiateResponse{TransactionID: "tx-1", PaymentLink: "https://pay.example.com/1"}, nil
}

func (g *p9a6ResaleGateway) Refund(context.Context, adapter.RefundRequest) (*adapter.RefundResponse, error) {
	return g.refundResp, g.refundErr
}

func (g *p9a6ResaleGateway) Payout(context.Context, adapter.PayoutRequest) (*adapter.PayoutResponse, error) {
	return g.payoutResp, g.payoutErr
}

func (g *p9a6ResaleGateway) GetStatus(context.Context, string) (*adapter.StatusResponse, error) {
	return g.statusResp, g.statusErr
}

func (g *p9a6ResaleGateway) ValidateWebhookSignature(map[string]string, []byte) error {
	return g.sigErr
}

// ============================================================
// P9a-6 helpers
// ============================================================

var (
	p9a6Seller = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	p9a6Buyer  = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	p9a6Ticket = uuid.MustParse("33333333-3333-3333-3333-333333333333")
	p9a6Event  = uuid.MustParse("44444444-4444-4444-4444-444444444444")
	p9a6Resale = uuid.MustParse("55555555-5555-5555-5555-555555555555")
)

func p9a6TicketInfo() *TicketForResale {
	return &TicketForResale{
		OwnerUserID: p9a6Seller, Status: "CONFIRMED",
		SectionPrice: "1000.00", EventID: p9a6Event,
	}
}

func p9a6Service(repo *p9a6ResaleRepo, db *p9a6ResaleDB, gw adapter.PaymentGateway) *ResaleService {
	return NewResaleService(repo, db, nil, gw, "0.05")
}

func p9a6Ctx(t *testing.T, method, target, body, userID string, params gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if userID != "" {
		c.Set("user_id", userID)
	}
	c.Params = params
	return c, w
}

// ============================================================
// Service: ListTicket
// ============================================================

func TestP9a6_ListTicket_Success(t *testing.T) {
	repo := &p9a6ResaleRepo{ticket: p9a6TicketInfo()}
	svc := p9a6Service(repo, &p9a6ResaleDB{}, nil)

	resp, err := svc.ListTicket(context.Background(), p9a6Seller, &ListTicketRequest{
		TicketID: p9a6Ticket, ResalePrice: "900.00",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, p9a6Ticket, resp.TicketID)
	assert.Equal(t, "1000.00", resp.OriginalPrice)
	assert.Equal(t, "900.00", resp.ResalePrice)
	assert.Equal(t, "45", resp.PlatformFee)
	assert.Equal(t, "855", resp.SellerPayout)
	assert.Equal(t, "PENDING", resp.Status)
	require.NotNil(t, repo.created)
	assert.Equal(t, p9a6Seller, repo.created.SellerUserID)
}

func TestP9a6_ListTicket_NotOwner_Forbidden(t *testing.T) {
	repo := &p9a6ResaleRepo{ticket: p9a6TicketInfo()}
	svc := p9a6Service(repo, &p9a6ResaleDB{}, nil)

	_, err := svc.ListTicket(context.Background(), p9a6Buyer, &ListTicketRequest{
		TicketID: p9a6Ticket, ResalePrice: "900.00",
	})
	assert.ErrorIs(t, err, ErrResaleForbidden)
}

func TestP9a6_ListTicket_TicketNotConfirmed(t *testing.T) {
	info := p9a6TicketInfo()
	info.Status = "USED"
	repo := &p9a6ResaleRepo{ticket: info}
	svc := p9a6Service(repo, &p9a6ResaleDB{}, nil)

	_, err := svc.ListTicket(context.Background(), p9a6Seller, &ListTicketRequest{
		TicketID: p9a6Ticket, ResalePrice: "900.00",
	})
	assert.ErrorIs(t, err, ErrResaleTicketNotConfirmed)
}

func TestP9a6_ListTicket_TicketInfoError(t *testing.T) {
	sentinel := errors.New("db down")
	repo := &p9a6ResaleRepo{ticketErr: sentinel}
	svc := p9a6Service(repo, &p9a6ResaleDB{}, nil)

	_, err := svc.ListTicket(context.Background(), p9a6Seller, &ListTicketRequest{
		TicketID: p9a6Ticket, ResalePrice: "900.00",
	})
	assert.ErrorIs(t, err, sentinel)
}

func TestP9a6_ListTicket_HasActiveListingError(t *testing.T) {
	sentinel := errors.New("db down")
	repo := &p9a6ResaleRepo{ticket: p9a6TicketInfo(), hasActiveErr: sentinel}
	svc := p9a6Service(repo, &p9a6ResaleDB{}, nil)

	_, err := svc.ListTicket(context.Background(), p9a6Seller, &ListTicketRequest{
		TicketID: p9a6Ticket, ResalePrice: "900.00",
	})
	assert.ErrorIs(t, err, sentinel)
}

// UT-RESALE-002: ListTicket_AlreadyListed_Returns409
func TestP9a6_ListTicket_ListingAlreadyExists(t *testing.T) {
	repo := &p9a6ResaleRepo{ticket: p9a6TicketInfo(), hasActive: true}
	svc := p9a6Service(repo, &p9a6ResaleDB{}, nil)

	_, err := svc.ListTicket(context.Background(), p9a6Seller, &ListTicketRequest{
		TicketID: p9a6Ticket, ResalePrice: "900.00",
	})
	assert.ErrorIs(t, err, ErrResaleListingExists)
}

func TestP9a6_ListTicket_CapQueryError(t *testing.T) {
	repo := &p9a6ResaleRepo{ticket: p9a6TicketInfo()}
	db := &p9a6ResaleDB{row: &p9a6ResaleRow{err: errors.New("no config row")}}
	svc := p9a6Service(repo, db, nil)

	_, err := svc.ListTicket(context.Background(), p9a6Seller, &ListTicketRequest{
		TicketID: p9a6Ticket, ResalePrice: "900.00",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no config row")
}

// UT-RESALE-001: ListTicket_PriceExceedsCap10_Returns422
func TestP9a6_ListTicket_PriceExceedsCap(t *testing.T) {
	repo := &p9a6ResaleRepo{ticket: p9a6TicketInfo()}
	db := &p9a6ResaleDB{row: &p9a6ResaleRow{capPercent: 10}}
	svc := p9a6Service(repo, db, nil)

	_, err := svc.ListTicket(context.Background(), p9a6Seller, &ListTicketRequest{
		TicketID: p9a6Ticket, ResalePrice: "1100.01",
	})
	assert.ErrorIs(t, err, ErrResalePriceExceedsCap)
}

func TestP9a6_ListTicket_PriceAtCapAllowed(t *testing.T) {
	repo := &p9a6ResaleRepo{ticket: p9a6TicketInfo()}
	db := &p9a6ResaleDB{row: &p9a6ResaleRow{capPercent: 10}}
	svc := p9a6Service(repo, db, nil)

	resp, err := svc.ListTicket(context.Background(), p9a6Seller, &ListTicketRequest{
		TicketID: p9a6Ticket, ResalePrice: "1100.00",
	})
	require.NoError(t, err)
	assert.Equal(t, "1100.00", resp.ResalePrice)
}

func TestP9a6_ListTicket_InvalidOriginalPrice(t *testing.T) {
	info := p9a6TicketInfo()
	info.SectionPrice = "not-a-number"
	repo := &p9a6ResaleRepo{ticket: info}
	svc := p9a6Service(repo, &p9a6ResaleDB{}, nil)

	_, err := svc.ListTicket(context.Background(), p9a6Seller, &ListTicketRequest{
		TicketID: p9a6Ticket, ResalePrice: "900.00",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse decimal")
}

func TestP9a6_ListTicket_InvalidResalePrice(t *testing.T) {
	repo := &p9a6ResaleRepo{ticket: p9a6TicketInfo()}
	svc := p9a6Service(repo, &p9a6ResaleDB{}, nil)

	_, err := svc.ListTicket(context.Background(), p9a6Seller, &ListTicketRequest{
		TicketID: p9a6Ticket, ResalePrice: "abc",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse decimal")
}

func TestP9a6_ListTicket_InvalidFeeRate(t *testing.T) {
	repo := &p9a6ResaleRepo{ticket: p9a6TicketInfo()}
	svc := NewResaleService(repo, &p9a6ResaleDB{}, nil, nil, "not-a-rate")

	_, err := svc.ListTicket(context.Background(), p9a6Seller, &ListTicketRequest{
		TicketID: p9a6Ticket, ResalePrice: "900.00",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse decimal")
}

func TestP9a6_ListTicket_CreateError(t *testing.T) {
	sentinel := errors.New("duplicate listing")
	repo := &p9a6ResaleRepo{ticket: p9a6TicketInfo(), createErr: sentinel}
	svc := p9a6Service(repo, &p9a6ResaleDB{}, nil)

	_, err := svc.ListTicket(context.Background(), p9a6Seller, &ListTicketRequest{
		TicketID: p9a6Ticket, ResalePrice: "900.00",
	})
	assert.ErrorIs(t, err, sentinel)
}

// ============================================================
// Service: Browse
// ============================================================

func TestP9a6_Browse_Success(t *testing.T) {
	repo := &p9a6ResaleRepo{listings: []BrowseListing{{
		ResaleID: p9a6Resale, TicketID: p9a6Ticket, SellerName: "Budi",
		Seat: "VIP - 1", ResalePrice: "900.00", PlatformFee: "45.00", SellerPayout: "855.00",
	}}}
	svc := p9a6Service(repo, &p9a6ResaleDB{}, nil)

	resp, err := svc.Browse(context.Background(), p9a6Event)
	require.NoError(t, err)
	require.Len(t, resp.Listings, 1)
	assert.Equal(t, "Budi", resp.Listings[0].SellerName)
}

func TestP9a6_Browse_RepositoryError(t *testing.T) {
	sentinel := errors.New("db down")
	repo := &p9a6ResaleRepo{listErr: sentinel}
	svc := p9a6Service(repo, &p9a6ResaleDB{}, nil)

	_, err := svc.Browse(context.Background(), p9a6Event)
	assert.ErrorIs(t, err, sentinel)
}

func TestP9a6_Browse_NoListings(t *testing.T) {
	repo := &p9a6ResaleRepo{}
	svc := p9a6Service(repo, &p9a6ResaleDB{}, nil)

	resp, err := svc.Browse(context.Background(), p9a6Event)
	require.NoError(t, err)
	assert.Empty(t, resp.Listings)
}

// ============================================================
// Service: BuyResaleTicket
// ============================================================

func p9a6PendingResale() *ResaleTransaction {
	return &ResaleTransaction{
		ID: p9a6Resale, TicketID: p9a6Ticket, EventID: p9a6Event,
		SellerUserID: p9a6Seller, ResalePrice: "900.00", Status: "PENDING",
	}
}

func TestP9a6_Buy_Success_WithoutGateway(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	tx := &p9a6ResaleTx{}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: tx}, nil)

	resp, err := svc.BuyResaleTicket(context.Background(), p9a6Buyer, p9a6Resale)
	require.NoError(t, err)
	assert.Equal(t, p9a6Resale, resp.ResaleID)
	assert.Equal(t, "RESERVED", resp.Status)
	assert.Contains(t, resp.PaymentLink, p9a6Resale.String())
	assert.True(t, resp.ReservationExpiresAt.After(time.Now().Add(100*time.Second)))
	assert.True(t, tx.committed)
	require.Len(t, repo.buyerSet, 1)
	assert.Equal(t, p9a6Buyer, repo.buyerSet[0])
}

func TestP9a6_Buy_Success_WithGateway(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	gw := &p9a6ResaleGateway{initiateResp: &adapter.InitiateResponse{
		TransactionID: "tx-99", PaymentLink: "https://pay.example.com/99",
	}}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, gw)

	resp, err := svc.BuyResaleTicket(context.Background(), p9a6Buyer, p9a6Resale)
	require.NoError(t, err)
	assert.Equal(t, "https://pay.example.com/99", resp.PaymentLink)
	require.Len(t, repo.paymentTxIDs, 1)
	assert.Equal(t, "tx-99", repo.paymentTxIDs[0])
	require.Len(t, gw.initiated, 1)
	assert.Equal(t, "resale:"+p9a6Resale.String(), gw.initiated[0].BookingID)
	assert.Equal(t, "900.00", gw.initiated[0].Amount)
}

func TestP9a6_Buy_BeginError(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	db := &p9a6ResaleDB{beginErr: errors.New("cannot connect")}
	svc := p9a6Service(repo, db, nil)

	_, err := svc.BuyResaleTicket(context.Background(), p9a6Buyer, p9a6Resale)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot connect")
}

func TestP9a6_Buy_NotFound(t *testing.T) {
	repo := &p9a6ResaleRepo{byIDErr: ErrResaleNotFound}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil)

	_, err := svc.BuyResaleTicket(context.Background(), p9a6Buyer, p9a6Resale)
	assert.ErrorIs(t, err, ErrResaleNotFound)
}

func TestP9a6_Buy_NotPending(t *testing.T) {
	rt := p9a6PendingResale()
	rt.Status = "COMPLETED"
	repo := &p9a6ResaleRepo{byID: rt}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil)

	_, err := svc.BuyResaleTicket(context.Background(), p9a6Buyer, p9a6Resale)
	assert.ErrorIs(t, err, ErrResaleNotPending)
}

func TestP9a6_Buy_AlreadyReserved(t *testing.T) {
	rt := p9a6PendingResale()
	buyer := p9a6Buyer
	rt.BuyerUserID = &buyer
	repo := &p9a6ResaleRepo{byID: rt}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil)

	_, err := svc.BuyResaleTicket(context.Background(), p9a6Buyer, p9a6Resale)
	assert.ErrorIs(t, err, ErrResaleAlreadyReserved)
}

// UT-RESALE-003: BuyResale_OwnListing_Returns422
func TestP9a6_Buy_OwnListing(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil)

	_, err := svc.BuyResaleTicket(context.Background(), p9a6Seller, p9a6Resale)
	assert.ErrorIs(t, err, ErrResaleOwnListing)
}

func TestP9a6_Buy_UpdateBuyerError(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale(), updateBuyerErr: errors.New("update failed")}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil)

	_, err := svc.BuyResaleTicket(context.Background(), p9a6Buyer, p9a6Resale)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update failed")
}

func TestP9a6_Buy_CommitError(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	tx := &p9a6ResaleTx{commitErr: errors.New("commit failed")}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: tx}, nil)

	_, err := svc.BuyResaleTicket(context.Background(), p9a6Buyer, p9a6Resale)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit failed")
}

func TestP9a6_Buy_GatewayError_ReleasesReservation(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	gw := &p9a6ResaleGateway{initiateErr: errors.New("gateway down")}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, gw)

	_, err := svc.BuyResaleTicket(context.Background(), p9a6Buyer, p9a6Resale)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway down")
	assert.Equal(t, 1, repo.clearBuyerCalls, "reservation must be released so buyer can retry")
}

func TestP9a6_Buy_GatewayError_ReleaseTxFails(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	gw := &p9a6ResaleGateway{initiateErr: errors.New("gateway down")}
	db := &p9a6ResaleDB{tx: &p9a6ResaleTx{}}
	svc := p9a6Service(repo, db, gw)

	_, err := svc.BuyResaleTicket(context.Background(), p9a6Buyer, p9a6Resale)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway down")
}

func TestP9a6_Buy_SetPaymentTransactionIDError(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale(), setPaymentErr: errors.New("persist failed")}
	gw := &p9a6ResaleGateway{}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, gw)

	_, err := svc.BuyResaleTicket(context.Background(), p9a6Buyer, p9a6Resale)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist failed")
}

// ============================================================
// Service: Cancel
// ============================================================

func TestP9a6_Cancel_Success(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	tx := &p9a6ResaleTx{}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: tx}, nil)

	resp, err := svc.Cancel(context.Background(), p9a6Seller, p9a6Resale)
	require.NoError(t, err)
	assert.Equal(t, p9a6Resale, resp.ResaleID)
	assert.Equal(t, "CANCELLED", resp.Status)
	assert.True(t, tx.committed)
	require.Len(t, repo.statusUpdates, 1)
	assert.Equal(t, "CANCELLED", repo.statusUpdates[0])
}

func TestP9a6_Cancel_BeginError(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	svc := p9a6Service(repo, &p9a6ResaleDB{beginErr: errors.New("cannot connect")}, nil)

	_, err := svc.Cancel(context.Background(), p9a6Seller, p9a6Resale)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot connect")
}

func TestP9a6_Cancel_NotFound(t *testing.T) {
	repo := &p9a6ResaleRepo{byIDErr: ErrResaleNotFound}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil)

	_, err := svc.Cancel(context.Background(), p9a6Seller, p9a6Resale)
	assert.ErrorIs(t, err, ErrResaleNotFound)
}

func TestP9a6_Cancel_NotSeller_Forbidden(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil)

	_, err := svc.Cancel(context.Background(), p9a6Buyer, p9a6Resale)
	assert.ErrorIs(t, err, ErrResaleForbidden)
}

func TestP9a6_Cancel_NotPending(t *testing.T) {
	rt := p9a6PendingResale()
	rt.Status = "COMPLETED"
	repo := &p9a6ResaleRepo{byID: rt}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil)

	_, err := svc.Cancel(context.Background(), p9a6Seller, p9a6Resale)
	assert.ErrorIs(t, err, ErrResaleNotPending)
}

func TestP9a6_Cancel_AlreadyReserved(t *testing.T) {
	rt := p9a6PendingResale()
	buyer := p9a6Buyer
	rt.BuyerUserID = &buyer
	repo := &p9a6ResaleRepo{byID: rt}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil)

	_, err := svc.Cancel(context.Background(), p9a6Seller, p9a6Resale)
	assert.ErrorIs(t, err, ErrResaleNotPending)
}

func TestP9a6_Cancel_UpdateStatusError(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale(), updateStatusErr: errors.New("update failed")}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil)

	_, err := svc.Cancel(context.Background(), p9a6Seller, p9a6Resale)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update failed")
}

func TestP9a6_Cancel_CommitError(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	tx := &p9a6ResaleTx{commitErr: errors.New("commit failed")}
	svc := p9a6Service(repo, &p9a6ResaleDB{tx: tx}, nil)

	_, err := svc.Cancel(context.Background(), p9a6Seller, p9a6Resale)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit failed")
}

// ============================================================
// Handler: List
// ============================================================

func p9a6ListBody(t *testing.T, ticketID uuid.UUID, price string) string {
	t.Helper()
	raw, err := json.Marshal(ListTicketRequest{TicketID: ticketID, ResalePrice: price})
	require.NoError(t, err)
	return string(raw)
}

func TestP9a6_Handler_List_MissingUser(t *testing.T) {
	h := NewResaleHandler(p9a6Service(&p9a6ResaleRepo{}, &p9a6ResaleDB{}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/list", p9a6ListBody(t, p9a6Ticket, "900.00"), "", nil)
	h.List(c)

	assert.Equal(t, 401, w.Code)
	assert.Contains(t, w.Body.String(), "UNAUTHORIZED")
}

func TestP9a6_Handler_List_InvalidBody(t *testing.T) {
	h := NewResaleHandler(p9a6Service(&p9a6ResaleRepo{}, &p9a6ResaleDB{}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/list", `{"broken":`, p9a6Seller.String(), nil)
	h.List(c)

	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_REQUEST")
}

func TestP9a6_Handler_List_Forbidden(t *testing.T) {
	repo := &p9a6ResaleRepo{ticket: p9a6TicketInfo()}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/list", p9a6ListBody(t, p9a6Ticket, "900.00"), p9a6Buyer.String(), nil)
	h.List(c)

	assert.Equal(t, 403, w.Code)
	assert.Contains(t, w.Body.String(), "FORBIDDEN")
}

func TestP9a6_Handler_List_ListingExists(t *testing.T) {
	repo := &p9a6ResaleRepo{ticket: p9a6TicketInfo(), hasActive: true}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/list", p9a6ListBody(t, p9a6Ticket, "900.00"), p9a6Seller.String(), nil)
	h.List(c)

	assert.Equal(t, 409, w.Code)
	assert.Contains(t, w.Body.String(), "RESALE_LISTING_EXISTS")
}

func TestP9a6_Handler_List_PriceExceedsCap(t *testing.T) {
	repo := &p9a6ResaleRepo{ticket: p9a6TicketInfo()}
	db := &p9a6ResaleDB{row: &p9a6ResaleRow{capPercent: 10}}
	h := NewResaleHandler(p9a6Service(repo, db, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/list", p9a6ListBody(t, p9a6Ticket, "1100.01"), p9a6Seller.String(), nil)
	h.List(c)

	assert.Equal(t, 422, w.Code)
	assert.Contains(t, w.Body.String(), "price exceeds cap")
}

func TestP9a6_Handler_List_TicketNotConfirmed(t *testing.T) {
	info := p9a6TicketInfo()
	info.Status = "CANCELLED"
	repo := &p9a6ResaleRepo{ticket: info}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/list", p9a6ListBody(t, p9a6Ticket, "900.00"), p9a6Seller.String(), nil)
	h.List(c)

	assert.Equal(t, 422, w.Code)
	assert.Contains(t, w.Body.String(), "ticket not confirmed")
}

func TestP9a6_Handler_List_InternalError(t *testing.T) {
	repo := &p9a6ResaleRepo{ticketErr: errors.New("db down")}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/list", p9a6ListBody(t, p9a6Ticket, "900.00"), p9a6Seller.String(), nil)
	h.List(c)

	assert.Equal(t, 500, w.Code)
	assert.Contains(t, w.Body.String(), "INTERNAL_ERROR")
}

func TestP9a6_Handler_List_Success(t *testing.T) {
	repo := &p9a6ResaleRepo{ticket: p9a6TicketInfo()}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/list", p9a6ListBody(t, p9a6Ticket, "900.00"), p9a6Seller.String(), nil)
	h.List(c)

	require.Equal(t, 201, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `"success":true`)
	assert.Contains(t, body, `"resale_price":"900.00"`)
	assert.Contains(t, body, `"meta"`)
	assert.Contains(t, body, `"request_id"`)
}

// ============================================================
// Handler: Browse
// ============================================================

func TestP9a6_Handler_Browse_InvalidEventID(t *testing.T) {
	h := NewResaleHandler(p9a6Service(&p9a6ResaleRepo{}, &p9a6ResaleDB{}, nil))

	c, w := p9a6Ctx(t, http.MethodGet, "/resale/browse?event_id=bad", "", "", nil)
	h.Browse(c)

	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_REQUEST")
}

func TestP9a6_Handler_Browse_Success(t *testing.T) {
	repo := &p9a6ResaleRepo{listings: []BrowseListing{{ResaleID: p9a6Resale, SellerName: "Budi"}}}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{}, nil))

	c, w := p9a6Ctx(t, http.MethodGet, "/resale/browse?event_id="+p9a6Event.String(), "", "", nil)
	h.Browse(c)

	require.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"listings"`)
	assert.Contains(t, w.Body.String(), "Budi")
}

func TestP9a6_Handler_Browse_InternalError(t *testing.T) {
	repo := &p9a6ResaleRepo{listErr: errors.New("db down")}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{}, nil))

	c, w := p9a6Ctx(t, http.MethodGet, "/resale/browse?event_id="+p9a6Event.String(), "", "", nil)
	h.Browse(c)

	assert.Equal(t, 500, w.Code)
}

// ============================================================
// Handler: Buy
// ============================================================

func TestP9a6_Handler_Buy_MissingUser(t *testing.T) {
	h := NewResaleHandler(p9a6Service(&p9a6ResaleRepo{}, &p9a6ResaleDB{}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/buy/"+p9a6Resale.String(), "", "",
		gin.Params{{Key: "id", Value: p9a6Resale.String()}})
	h.Buy(c)

	assert.Equal(t, 401, w.Code)
}

func TestP9a6_Handler_Buy_InvalidID(t *testing.T) {
	h := NewResaleHandler(p9a6Service(&p9a6ResaleRepo{}, &p9a6ResaleDB{}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/buy/bad", "", p9a6Buyer.String(),
		gin.Params{{Key: "id", Value: "bad"}})
	h.Buy(c)

	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "invalid resale_id")
}

func TestP9a6_Handler_Buy_NotFound(t *testing.T) {
	repo := &p9a6ResaleRepo{byIDErr: ErrResaleNotFound}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/buy/"+p9a6Resale.String(), "", p9a6Buyer.String(),
		gin.Params{{Key: "id", Value: p9a6Resale.String()}})
	h.Buy(c)

	assert.Equal(t, 404, w.Code)
}

func TestP9a6_Handler_Buy_AlreadyReserved(t *testing.T) {
	rt := p9a6PendingResale()
	buyer := p9a6Buyer
	rt.BuyerUserID = &buyer
	repo := &p9a6ResaleRepo{byID: rt}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/buy/"+p9a6Resale.String(), "", p9a6Buyer.String(),
		gin.Params{{Key: "id", Value: p9a6Resale.String()}})
	h.Buy(c)

	assert.Equal(t, 409, w.Code)
	assert.Contains(t, w.Body.String(), "already reserved")
}

func TestP9a6_Handler_Buy_NotPending(t *testing.T) {
	rt := p9a6PendingResale()
	rt.Status = "COMPLETED"
	repo := &p9a6ResaleRepo{byID: rt}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/buy/"+p9a6Resale.String(), "", p9a6Buyer.String(),
		gin.Params{{Key: "id", Value: p9a6Resale.String()}})
	h.Buy(c)

	assert.Equal(t, 409, w.Code)
	assert.Contains(t, w.Body.String(), "not pending")
}

func TestP9a6_Handler_Buy_OwnListing(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/buy/"+p9a6Resale.String(), "", p9a6Seller.String(),
		gin.Params{{Key: "id", Value: p9a6Resale.String()}})
	h.Buy(c)

	assert.Equal(t, 422, w.Code)
	assert.Contains(t, w.Body.String(), "cannot buy own listing")
}

func TestP9a6_Handler_Buy_Success(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/buy/"+p9a6Resale.String(), "", p9a6Buyer.String(),
		gin.Params{{Key: "id", Value: p9a6Resale.String()}})
	h.Buy(c)

	require.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"RESERVED"`)
	assert.Contains(t, w.Body.String(), `"payment_link"`)
}

// ============================================================
// Handler: Cancel
// ============================================================

func TestP9a6_Handler_Cancel_MissingUser(t *testing.T) {
	h := NewResaleHandler(p9a6Service(&p9a6ResaleRepo{}, &p9a6ResaleDB{}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/cancel/"+p9a6Resale.String(), "", "",
		gin.Params{{Key: "id", Value: p9a6Resale.String()}})
	h.Cancel(c)

	assert.Equal(t, 401, w.Code)
}

func TestP9a6_Handler_Cancel_InvalidID(t *testing.T) {
	h := NewResaleHandler(p9a6Service(&p9a6ResaleRepo{}, &p9a6ResaleDB{}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/cancel/bad", "", p9a6Seller.String(),
		gin.Params{{Key: "id", Value: "bad"}})
	h.Cancel(c)

	assert.Equal(t, 400, w.Code)
}

func TestP9a6_Handler_Cancel_NotSeller(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/cancel/"+p9a6Resale.String(), "", p9a6Buyer.String(),
		gin.Params{{Key: "id", Value: p9a6Resale.String()}})
	h.Cancel(c)

	assert.Equal(t, 403, w.Code)
	assert.Contains(t, w.Body.String(), "not seller")
}

func TestP9a6_Handler_Cancel_NotPending(t *testing.T) {
	rt := p9a6PendingResale()
	rt.Status = "COMPLETED"
	repo := &p9a6ResaleRepo{byID: rt}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/cancel/"+p9a6Resale.String(), "", p9a6Seller.String(),
		gin.Params{{Key: "id", Value: p9a6Resale.String()}})
	h.Cancel(c)

	assert.Equal(t, 422, w.Code)
	assert.Contains(t, w.Body.String(), "cannot cancel")
}

func TestP9a6_Handler_Cancel_Success(t *testing.T) {
	repo := &p9a6ResaleRepo{byID: p9a6PendingResale()}
	h := NewResaleHandler(p9a6Service(repo, &p9a6ResaleDB{tx: &p9a6ResaleTx{}}, nil))

	c, w := p9a6Ctx(t, http.MethodPost, "/resale/cancel/"+p9a6Resale.String(), "", p9a6Seller.String(),
		gin.Params{{Key: "id", Value: p9a6Resale.String()}})
	h.Cancel(c)

	require.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"CANCELLED"`)
}

// ============================================================
// Idempotency middleware
// ============================================================

func p9a6IdemRedis(t *testing.T) *redisclient.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	client := &redisclient.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestP9a6_Idempotency_NoKeyPassesThrough(t *testing.T) {
	client := p9a6IdemRedis(t)
	router := gin.New()
	router.Use(IdempotencyMiddleware(client, time.Hour))
	router.POST("/x", func(c *gin.Context) { c.String(http.StatusOK, "fresh") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "fresh", w.Body.String())
}

func TestP9a6_Idempotency_NilRedisPassesThrough(t *testing.T) {
	router := gin.New()
	router.Use(IdempotencyMiddleware(nil, time.Hour))
	router.POST("/x", func(c *gin.Context) { c.String(http.StatusOK, "fresh") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("X-Idempotency-Key", "key-1")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "fresh", w.Body.String())
}

func TestP9a6_Idempotency_ReplaysCachedBody(t *testing.T) {
	client := p9a6IdemRedis(t)
	calls := 0
	router := gin.New()
	router.Use(IdempotencyMiddleware(client, time.Hour))
	router.POST("/x", func(c *gin.Context) {
		calls++
		c.String(http.StatusCreated, "body-%d", calls)
	})

	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/x", nil)
	req1.Header.Set("X-Idempotency-Key", "key-1")
	router.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusCreated, w1.Code)
	assert.Equal(t, "body-1", w1.Body.String())

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/x", nil)
	req2.Header.Set("X-Idempotency-Key", "key-1")
	router.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "body-1", w2.Body.String())
	assert.Equal(t, 1, calls, "handler must not run twice for the same key")
}

func TestP9a6_Idempotency_DoesNotCacheErrors(t *testing.T) {
	client := p9a6IdemRedis(t)
	calls := 0
	router := gin.New()
	router.Use(IdempotencyMiddleware(client, time.Hour))
	router.POST("/x", func(c *gin.Context) {
		calls++
		c.String(http.StatusInternalServerError, "boom")
	})

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		req.Header.Set("X-Idempotency-Key", "key-err")
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	}
	assert.Equal(t, 2, calls)
}

func TestP9a6_Idempotency_DifferentKeysAreIndependent(t *testing.T) {
	client := p9a6IdemRedis(t)
	calls := 0
	router := gin.New()
	router.Use(IdempotencyMiddleware(client, time.Hour))
	router.POST("/x", func(c *gin.Context) {
		calls++
		c.String(http.StatusOK, "n=%d", calls)
	})

	for _, key := range []string{"a", "b"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		req.Header.Set("X-Idempotency-Key", key)
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}
	assert.Equal(t, 2, calls)
}

func TestP9a6_Idempotency_ResponseCapture_WritesThrough(t *testing.T) {
	client := p9a6IdemRedis(t)
	router := gin.New()
	router.Use(IdempotencyMiddleware(client, time.Hour))
	router.POST("/x", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("X-Idempotency-Key", "key-json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok":true`)
}

// ============================================================
// Envelope, model, decimal helpers
// ============================================================

func TestP9a6_ResaleMeta_HasTimestampAndRequestID(t *testing.T) {
	meta := resaleMeta()
	require.Contains(t, meta, "timestamp")
	require.Contains(t, meta, "request_id")
	assert.NotEmpty(t, meta["request_id"])
}

func TestP9a6_SafeLog_ReturnsJSON(t *testing.T) {
	rt := &ResaleTransaction{ID: p9a6Resale, Status: "PENDING"}
	out, err := rt.SafeLog()
	require.NoError(t, err)
	assert.Contains(t, out, p9a6Resale.String())
	assert.Contains(t, out, "PENDING")
}

func TestP9a6_ParseDecimal_Valid(t *testing.T) {
	d, err := parseDecimal("123.45")
	require.NoError(t, err)
	assert.Equal(t, "123.45", d.String())
}

func TestP9a6_ParseDecimal_Invalid(t *testing.T) {
	_, err := parseDecimal("abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resale: parse decimal")
}

func TestP9a6_ResaleSentinels_Distinct(t *testing.T) {
	sentinels := []error{
		ErrResaleNotFound, ErrResaleForbidden, ErrResaleListingExists,
		ErrResaleNotPending, ErrResaleAlreadyReserved, ErrResaleOwnListing,
		ErrResalePriceExceedsCap, ErrResaleTicketNotConfirmed,
	}
	for i := 0; i < len(sentinels); i++ {
		for j := i + 1; j < len(sentinels); j++ {
			assert.NotEqual(t, sentinels[i].Error(), sentinels[j].Error())
		}
	}
}

func TestP9a6_ResaleTransaction_JSONShape(t *testing.T) {
	rt := ResaleTransaction{
		ID: p9a6Resale, TicketID: p9a6Ticket, EventID: p9a6Event,
		SellerUserID: p9a6Seller, OriginalPrice: "1000.00", ResalePrice: "900.00",
		PlatformFee: "45.00", SellerPayout: "855.00", Status: "PENDING",
	}
	raw, err := json.Marshal(rt)
	require.NoError(t, err)
	body := string(raw)
	assert.Contains(t, body, `"resale_price":"900.00"`)
	assert.Contains(t, body, `"platform_fee":"45.00"`)
	assert.Contains(t, body, `"seller_payout":"855.00"`)
	assert.Contains(t, body, `"status":"PENDING"`)
}

func TestP9a6_ListTicketResponse_JSONShape(t *testing.T) {
	resp := ListTicketResponse{
		ResaleID: p9a6Resale, TicketID: p9a6Ticket, OriginalPrice: "1000.00",
		ResalePrice: "900.00", PlatformFee: "45.00", SellerPayout: "855.00",
		Status: "PENDING", CreatedAt: time.Now(),
	}
	raw, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"resale_id"`)
	assert.Contains(t, string(raw), `"seller_payout":"855.00"`)
}

func TestP9a6_BuyResponse_JSONShape(t *testing.T) {
	resp := BuyResponse{
		ResaleID: p9a6Resale, Status: "RESERVED",
		ReservationExpiresAt: time.Now(), PaymentLink: "https://pay/x",
	}
	raw, err := json.Marshal(resp)
	require.NoError(t, err)
	body := string(raw)
	assert.Contains(t, body, `"status":"RESERVED"`)
	assert.Contains(t, body, `"payment_link":"https://pay/x"`)
}

func TestP9a6_ResaleExpiryPayload_JSONShape(t *testing.T) {
	raw, err := json.Marshal(ResaleExpiryPayload{ResaleID: p9a6Resale})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"resale_id"`)
}

func TestP9a6_PayoutPayload_JSONShape(t *testing.T) {
	raw, err := json.Marshal(PayoutPayload{
		ResaleID: p9a6Resale, SellerPayout: "855.00", SellerUserID: p9a6Seller,
	})
	require.NoError(t, err)
	body := string(raw)
	assert.Contains(t, body, `"seller_payout":"855.00"`)
	assert.Contains(t, body, `"seller_user_id"`)
}
