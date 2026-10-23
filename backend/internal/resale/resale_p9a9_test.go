package resale

// FIX P9a-9b TD-F7-01: resale service/helper edge coverage boost.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
)

type p9a9ResaleRepo struct {
	ticket          *TicketForResale
	ticketErr       error
	hasActive       bool
	hasActiveErr    error
	// capPercent      int
	// capErr          error
	createErr       error
	listPending     []BrowseListing
	listErr         error
	getByID         *ResaleTransaction
	getByIDErr      error
	getByIDFU       *ResaleTransaction
	getByIDFUErr    error
	updateBuyerErr  error
	clearBuyerErr   error
	updateStatusErr error
	setPayErr       error
	getByPay        *ResaleTransaction
	getByPayErr     error
}

func (r *p9a9ResaleRepo) TicketInfoForResale(context.Context, uuid.UUID) (*TicketForResale, error) {
	return r.ticket, r.ticketErr
}
func (r *p9a9ResaleRepo) HasActiveListing(context.Context, uuid.UUID) (bool, error) {
	return r.hasActive, r.hasActiveErr
}
func (r *p9a9ResaleRepo) Create(context.Context, *ResaleTransaction) error { return r.createErr }
func (r *p9a9ResaleRepo) ListPending(context.Context, uuid.UUID) ([]BrowseListing, error) {
	return r.listPending, r.listErr
}
func (r *p9a9ResaleRepo) GetByID(context.Context, uuid.UUID) (*ResaleTransaction, error) {
	return r.getByID, r.getByIDErr
}
func (r *p9a9ResaleRepo) GetByIDForUpdate(context.Context, pgx.Tx, uuid.UUID) (*ResaleTransaction, error) {
	return r.getByIDFU, r.getByIDFUErr
}
func (r *p9a9ResaleRepo) UpdateBuyer(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	return r.updateBuyerErr
}
func (r *p9a9ResaleRepo) ClearBuyer(context.Context, pgx.Tx, uuid.UUID) error {
	return r.clearBuyerErr
}
func (r *p9a9ResaleRepo) UpdateStatus(context.Context, pgx.Tx, uuid.UUID, string) error {
	return r.updateStatusErr
}
func (r *p9a9ResaleRepo) SetPaymentTransactionID(context.Context, uuid.UUID, string) error {
	return r.setPayErr
}
func (r *p9a9ResaleRepo) GetByPaymentTransactionID(context.Context, string) (*ResaleTransaction, error) {
	return r.getByPay, r.getByPayErr
}

type p9a9ResaleTx struct {
	pgx.Tx
	commitErr   error
	rollbackErr error
}

func (t *p9a9ResaleTx) Commit(context.Context) error   { return t.commitErr }
func (t *p9a9ResaleTx) Rollback(context.Context) error { return t.rollbackErr }

type p9a9ResaleDB struct {
	beginErr error
	tx       *p9a9ResaleTx
	capRow   *p9a9ResaleRow
}

func (d *p9a9ResaleDB) Begin(context.Context) (pgx.Tx, error) {
	if d.beginErr != nil {
		return nil, d.beginErr
	}
	if d.tx == nil {
		d.tx = &p9a9ResaleTx{}
	}
	return d.tx, nil
}
func (d *p9a9ResaleDB) QueryRow(_ context.Context, _ string, _ ...interface{}) pgx.Row {
	if d.capRow != nil {
		return d.capRow
	}
	return &p9a9ResaleRow{val: "10"}
}

type p9a9ResaleRow struct {
	val string
	err error
}

func (r *p9a9ResaleRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) > 0 {
		if p, ok := dest[0].(*int); ok {
			*p = 10
			return nil
		}
	}
	return nil
}

type p9a9ResaleGW struct {
	initErr    error
	initResp   *adapter.InitiateResponse
	refundErr  error
	payoutErr  error
	statusResp *adapter.StatusResponse
	statusErr  error
	sigErr     error
}

func (g *p9a9ResaleGW) Initiate(context.Context, adapter.InitiateRequest) (*adapter.InitiateResponse, error) {
	if g.initErr != nil {
		return nil, g.initErr
	}
	if g.initResp == nil {
		return &adapter.InitiateResponse{TransactionID: "tx1", PaymentLink: "https://pay"}, nil
	}
	return g.initResp, nil
}
func (g *p9a9ResaleGW) Refund(context.Context, adapter.RefundRequest) (*adapter.RefundResponse, error) {
	return &adapter.RefundResponse{}, g.refundErr
}
func (g *p9a9ResaleGW) Payout(context.Context, adapter.PayoutRequest) (*adapter.PayoutResponse, error) {
	return &adapter.PayoutResponse{}, g.payoutErr
}
func (g *p9a9ResaleGW) GetStatus(context.Context, string) (*adapter.StatusResponse, error) {
	return g.statusResp, g.statusErr
}
func (g *p9a9ResaleGW) ValidateWebhookSignature(map[string]string, []byte) error { return g.sigErr }

func TestP9a9ResaleParseDecimal_Edge(t *testing.T) {
	_, err := parseDecimal("")
	assert.Error(t, err)
	d, err := parseDecimal("100000.00")
	require.NoError(t, err)
	z, _ := parseDecimal("0")
	assert.True(t, d.GreaterThan(z))
}

func TestP9a9ResaleListTicket_CapQueryError(t *testing.T) {
	repo := &p9a9ResaleRepo{ticket: &TicketForResale{OwnerUserID: uuid.New(), Status: "CONFIRMED", SectionPrice: "100000", EventID: uuid.New()}}
	db := &p9a9ResaleDB{capRow: &p9a9ResaleRow{err: errors.New("db")}}
	svc := NewResaleService(repo, db, nil, nil, "0.025")
	_, err := svc.ListTicket(context.Background(), repo.ticket.OwnerUserID, &ListTicketRequest{TicketID: uuid.New(), ResalePrice: "105000"})
	require.Error(t, err)
}

func TestP9a9ResaleListTicket_InvalidOriginal(t *testing.T) {
	repo := &p9a9ResaleRepo{ticket: &TicketForResale{OwnerUserID: uuid.New(), Status: "CONFIRMED", SectionPrice: "bad", EventID: uuid.New()}}
	db := &p9a9ResaleDB{}
	svc := NewResaleService(repo, db, nil, nil, "0.025")
	_, err := svc.ListTicket(context.Background(), repo.ticket.OwnerUserID, &ListTicketRequest{TicketID: uuid.New(), ResalePrice: "105000"})
	require.Error(t, err)
}

func TestP9a9ResaleListTicket_InvalidResale(t *testing.T) {
	repo := &p9a9ResaleRepo{ticket: &TicketForResale{OwnerUserID: uuid.New(), Status: "CONFIRMED", SectionPrice: "100000", EventID: uuid.New()}}
	db := &p9a9ResaleDB{}
	svc := NewResaleService(repo, db, nil, nil, "0.025")
	_, err := svc.ListTicket(context.Background(), repo.ticket.OwnerUserID, &ListTicketRequest{TicketID: uuid.New(), ResalePrice: "bad"})
	require.Error(t, err)
}

func TestP9a9ResaleListTicket_InvalidFeeRate(t *testing.T) {
	repo := &p9a9ResaleRepo{ticket: &TicketForResale{OwnerUserID: uuid.New(), Status: "CONFIRMED", SectionPrice: "100000", EventID: uuid.New()}}
	db := &p9a9ResaleDB{}
	svc := NewResaleService(repo, db, nil, nil, "bad")
	_, err := svc.ListTicket(context.Background(), repo.ticket.OwnerUserID, &ListTicketRequest{TicketID: uuid.New(), ResalePrice: "105000"})
	require.Error(t, err)
}

func TestP9a9ResaleListTicket_CreateError(t *testing.T) {
	repo := &p9a9ResaleRepo{ticket: &TicketForResale{OwnerUserID: uuid.New(), Status: "CONFIRMED", SectionPrice: "100000", EventID: uuid.New()}, createErr: errors.New("x")}
	db := &p9a9ResaleDB{}
	svc := NewResaleService(repo, db, nil, nil, "0.025")
	_, err := svc.ListTicket(context.Background(), repo.ticket.OwnerUserID, &ListTicketRequest{TicketID: uuid.New(), ResalePrice: "105000"})
	require.Error(t, err)
}

func TestP9a9ResaleBrowse_Error(t *testing.T) {
	repo := &p9a9ResaleRepo{listErr: errors.New("x")}
	db := &p9a9ResaleDB{}
	svc := NewResaleService(repo, db, nil, nil, "0.025")
	_, err := svc.Browse(context.Background(), uuid.New())
	require.Error(t, err)
}

func TestP9a9ResaleBuy_BeginError(t *testing.T) {
	repo := &p9a9ResaleRepo{getByIDFU: &ResaleTransaction{ID: uuid.New(), SellerUserID: uuid.New(), Status: "PENDING"}}
	db := &p9a9ResaleDB{beginErr: errors.New("x")}
	svc := NewResaleService(repo, db, nil, nil, "0.025")
	_, err := svc.BuyResaleTicket(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
}

func TestP9a9ResaleBuy_UpdateBuyerError(t *testing.T) {
	seller := uuid.New()
	repo := &p9a9ResaleRepo{getByIDFU: &ResaleTransaction{ID: uuid.New(), SellerUserID: seller, Status: "PENDING"}, updateBuyerErr: errors.New("x")}
	db := &p9a9ResaleDB{}
	svc := NewResaleService(repo, db, nil, nil, "0.025")
	_, err := svc.BuyResaleTicket(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
}

func TestP9a9ResaleBuy_CommitError(t *testing.T) {
	seller := uuid.New()
	repo := &p9a9ResaleRepo{getByIDFU: &ResaleTransaction{ID: uuid.New(), SellerUserID: seller, Status: "PENDING"}}
	db := &p9a9ResaleDB{tx: &p9a9ResaleTx{commitErr: errors.New("x")}}
	svc := NewResaleService(repo, db, nil, nil, "0.025")
	_, err := svc.BuyResaleTicket(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
}

func TestP9a9ResaleBuy_GatewayReleaseFails(t *testing.T) {
	seller := uuid.New()
	rt := &ResaleTransaction{ID: uuid.New(), SellerUserID: seller, Status: "PENDING", ResalePrice: "100000"}
	repo := &p9a9ResaleRepo{getByIDFU: rt}
	db := &p9a9ResaleDB{tx: &p9a9ResaleTx{}}
	svc := NewResaleService(repo, db, nil, &p9a9ResaleGW{initErr: errors.New("gw")}, "0.025")
	_, err := svc.BuyResaleTicket(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
}

func TestP9a9ResaleBuy_SetPaymentErr(t *testing.T) {
	seller := uuid.New()
	rt := &ResaleTransaction{ID: uuid.New(), SellerUserID: seller, Status: "PENDING", ResalePrice: "100000"}
	repo := &p9a9ResaleRepo{getByIDFU: rt, setPayErr: errors.New("x")}
	db := &p9a9ResaleDB{tx: &p9a9ResaleTx{}}
	svc := NewResaleService(repo, db, nil, &p9a9ResaleGW{initResp: &adapter.InitiateResponse{TransactionID: "tx1", PaymentLink: "l"}}, "0.025")
	_, err := svc.BuyResaleTicket(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
}

func TestP9a9ResaleCancel_BeginErr(t *testing.T) {
	repo := &p9a9ResaleRepo{}
	db := &p9a9ResaleDB{beginErr: errors.New("x")}
	svc := NewResaleService(repo, db, nil, nil, "0.025")
	_, err := svc.Cancel(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
}

func TestP9a9ResaleCancel_GetForUpdateErr(t *testing.T) {
	repo := &p9a9ResaleRepo{getByIDFUErr: errors.New("x")}
	db := &p9a9ResaleDB{}
	svc := NewResaleService(repo, db, nil, nil, "0.025")
	_, err := svc.Cancel(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
}

func TestP9a9ResaleCancel_GetErr(t *testing.T) {
	repo := &p9a9ResaleRepo{getByIDFUErr: errors.New("x")}
	db := &p9a9ResaleDB{}
	svc := NewResaleService(repo, db, nil, nil, "0.025")
	_, err := svc.Cancel(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
}

func TestP9a9ResaleCancel_UpdateStatusErr(t *testing.T) {
	seller := uuid.New()
	rt := &ResaleTransaction{ID: uuid.New(), SellerUserID: seller, Status: "PENDING"}
	repo := &p9a9ResaleRepo{getByIDFU: rt, updateStatusErr: errors.New("x")}
	db := &p9a9ResaleDB{}
	svc := NewResaleService(repo, db, nil, nil, "0.025")
	_, err := svc.Cancel(context.Background(), seller, rt.ID)
	require.Error(t, err)
}

func TestP9a9ResaleCancel_CommitErr(t *testing.T) {
	seller := uuid.New()
	rt := &ResaleTransaction{ID: uuid.New(), SellerUserID: seller, Status: "PENDING"}
	repo := &p9a9ResaleRepo{getByIDFU: rt}
	db := &p9a9ResaleDB{tx: &p9a9ResaleTx{commitErr: errors.New("x")}}
	svc := NewResaleService(repo, db, nil, nil, "0.025")
	_, err := svc.Cancel(context.Background(), seller, rt.ID)
	require.Error(t, err)
}
