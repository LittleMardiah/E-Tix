package payment

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
)

// ============================================================
// Extra payment service tests — P9a-5
// Covers the previously-untested webhook pipeline:
// HandleWebhook, classifyWebhookStatus, handleResaleWebhook,
// buildInitiateResponseFromObligation and SetResaleWebhookDeps.
// ============================================================

// sigGateway adds a programmable signature-validation failure on top of the
// shared fakeGateway.
type sigGateway struct {
	fakeGateway
	sigErr error
}

func (g *sigGateway) ValidateWebhookSignature(_ map[string]string, _ []byte) error {
	return g.sigErr
}

// fakeResaleRepo implements ResaleWebhookLookup.
type fakeResaleRepo struct {
	tx        *ResaleTxLite
	getErr    error
	updatedID uuid.UUID
	updated   string
	updates   int
}

func (f *fakeResaleRepo) GetByPaymentTransactionID(_ context.Context, txID string) (*ResaleTxLite, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.tx == nil || f.tx.PaymentTransactionID == nil || *f.tx.PaymentTransactionID != txID {
		return nil, ErrWebhookNotFound
	}
	return f.tx, nil
}

func (f *fakeResaleRepo) UpdateStatus(_ context.Context, _ pgx.Tx, id uuid.UUID, status string) error {
	f.updatedID = id
	f.updated = status
	f.updates++
	return nil
}

// newPayEnv builds a PaymentService with injectable collaborators.
func newPayEnv(t *testing.T, repo PaymentObligationRepository, reader BookingReader, gw adapter.PaymentGateway) (*PaymentService, *fakeBeginner, *IdempotencyService) {
	t.Helper()
	beginner := &fakeBeginner{tx: &fakeTx{}}
	idem := NewIdempotencyService(newTestRedis(t), repo)
	svc := NewPaymentService(beginner, repo, reader, gw, idem)
	return svc, beginner, idem
}

// webhookPayload builds a signed webhook body.
func webhookPayload(t *testing.T, txID, status string) []byte {
	t.Helper()
	b, err := json.Marshal(WebhookPayload{ExternalTransactionID: txID, Status: status})
	require.NoError(t, err)
	return b
}

// seedObligation inserts a pending obligation carrying an external tx id.
func seedObligation(t *testing.T, repo *memObligationRepo, txID, status string) *PaymentObligation {
	t.Helper()
	o := &PaymentObligation{
		ID:                    uuid.New(),
		BookingID:             uuid.New(),
		UserID:                uuid.New(),
		Amount:                "1000.00",
		Status:                status,
		ExternalTransactionID: &txID,
	}
	require.NoError(t, repo.Create(context.Background(), o))
	return o
}

// ============================================================
// HandleWebhook
// ============================================================

func TestHandleWebhook_InvalidSignatureIsRejected(t *testing.T) {
	repo := &memObligationRepo{}
	gw := &sigGateway{sigErr: errors.New("bad signature")}
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, gw)

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-1", "SUCCESS"),
		map[string]string{"X-Gateway-Signature": "nope"})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWebhookInvalidSig)
}

func TestHandleWebhook_MalformedPayloadIsRejected(t *testing.T) {
	repo := &memObligationRepo{}
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})

	err := svc.HandleWebhook(context.Background(), []byte(`{not-json`), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal webhook")
}

func TestHandleWebhook_MissingExternalTransactionIDIsRejected(t *testing.T) {
	repo := &memObligationRepo{}
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, "", "SUCCESS"), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing external_transaction_id")
}

func TestHandleWebhook_TransitionalStatusIsAckedWithoutMutation(t *testing.T) {
	repo := &memObligationRepo{}
	ob := seedObligation(t, repo, "tx-pending", StatusPending)
	svc, tx, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-pending", "PENDING"), nil)

	require.NoError(t, err, "transitional statuses must be ACKed")
	assert.Equal(t, StatusPending, ob.Status, "no DB mutation for transitional statuses")
	assert.False(t, tx.tx.committed)
}

func TestHandleWebhook_ChallengeIsTransitional(t *testing.T) {
	repo := &memObligationRepo{}
	ob := seedObligation(t, repo, "tx-chal", StatusPending)
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})

	require.NoError(t, svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-chal", "CHALLENGE"), nil))
	assert.Equal(t, StatusPending, ob.Status)
}

func TestHandleWebhook_SuccessMarksPaid(t *testing.T) {
	repo := &memObligationRepo{}
	ob := seedObligation(t, repo, "tx-success", StatusPending)
	svc, tx, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-success", "SUCCESS"), nil)

	require.NoError(t, err)
	assert.Equal(t, StatusPaid, ob.Status)
	assert.True(t, tx.tx.committed, "a terminal status must commit")
}

func TestHandleWebhook_SettlementMarksPaid(t *testing.T) {
	repo := &memObligationRepo{}
	ob := seedObligation(t, repo, "tx-settle", StatusPending)
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})

	require.NoError(t, svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-settle", "SETTLEMENT"), nil))
	assert.Equal(t, StatusPaid, ob.Status)
}

func TestHandleWebhook_CaptureMarksPaid(t *testing.T) {
	repo := &memObligationRepo{}
	ob := seedObligation(t, repo, "tx-capture", StatusPending)
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})

	require.NoError(t, svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-capture", "CAPTURE"), nil))
	assert.Equal(t, StatusPaid, ob.Status)
}

func TestHandleWebhook_DenyMarksFailed(t *testing.T) {
	repo := &memObligationRepo{}
	ob := seedObligation(t, repo, "tx-deny", StatusPending)
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})

	require.NoError(t, svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-deny", "DENY"), nil))
	assert.Equal(t, StatusFailed, ob.Status)
}

func TestHandleWebhook_ExpireMarksFailed(t *testing.T) {
	repo := &memObligationRepo{}
	ob := seedObligation(t, repo, "tx-expire", StatusPending)
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})

	require.NoError(t, svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-expire", "EXPIRE"), nil))
	assert.Equal(t, StatusFailed, ob.Status)
}

func TestHandleWebhook_DuplicatePaidWebhookIsNoOp(t *testing.T) {
	repo := &memObligationRepo{}
	ob := seedObligation(t, repo, "tx-dup", StatusPaid)
	svc, tx, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-dup", "SUCCESS"), nil)

	require.NoError(t, err)
	assert.Equal(t, StatusPaid, ob.Status)
	assert.False(t, tx.tx.committed, "a duplicate PAID webhook must not open a write transaction")
}

func TestHandleWebhook_PaidIsNotDowngradedByFailure(t *testing.T) {
	repo := &memObligationRepo{}
	ob := seedObligation(t, repo, "tx-keep", StatusPaid)
	svc, tx, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-keep", "DENY"), nil)

	require.NoError(t, err)
	assert.Equal(t, StatusPaid, ob.Status, "a late failure must not downgrade a paid obligation")
	assert.False(t, tx.tx.committed)
}

func TestHandleWebhook_UnknownTransactionReturnsError(t *testing.T) {
	repo := &memObligationRepo{}
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-unknown", "SUCCESS"), nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWebhookNotFound)
}

func TestHandleWebhook_ResaleBranchMarksResaleCompleted(t *testing.T) {
	repo := &memObligationRepo{}
	txID := "resale-tx-1"
	resaleTx := &ResaleTxLite{
		ID:                   uuid.New(),
		SellerUserID:         uuid.New(),
		Status:               "PENDING",
		PaymentTransactionID: &txID,
		SellerPayout:         "9000",
	}
	rr := &fakeResaleRepo{tx: resaleTx}
	svc, tx, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})
	svc.SetResaleWebhookDeps(rr, nil)

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, txID, "SUCCESS"), nil)

	require.NoError(t, err)
	assert.Equal(t, 1, rr.updates)
	assert.Equal(t, "COMPLETED", rr.updated)
	assert.Equal(t, resaleTx.ID, rr.updatedID)
	assert.True(t, tx.tx.committed)
}

func TestHandleWebhook_ResaleBranchTransitionalIsAcked(t *testing.T) {
	repo := &memObligationRepo{}
	txID := "resale-tx-2"
	resaleTx := &ResaleTxLite{
		ID:                   uuid.New(),
		Status:               "PENDING",
		PaymentTransactionID: &txID,
	}
	rr := &fakeResaleRepo{tx: resaleTx}
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})
	svc.SetResaleWebhookDeps(rr, nil)

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, txID, "PENDING"), nil)

	require.NoError(t, err)
	assert.Equal(t, 0, rr.updates, "transitional resale statuses must not mutate the row")
}

func TestHandleWebhook_ResaleBranchAlreadyTerminalIsNoOp(t *testing.T) {
	repo := &memObligationRepo{}
	txID := "resale-tx-3"
	resaleTx := &ResaleTxLite{
		ID:                   uuid.New(),
		Status:               "COMPLETED",
		PaymentTransactionID: &txID,
	}
	rr := &fakeResaleRepo{tx: resaleTx}
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})
	svc.SetResaleWebhookDeps(rr, nil)

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, txID, "SUCCESS"), nil)

	require.NoError(t, err)
	assert.Equal(t, 0, rr.updates)
}

// ============================================================
// classifyWebhookStatus
// ============================================================

func TestClassifyWebhookStatus_Mapping(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"SUCCESS", StatusPaid},
		{"success", StatusPaid},
		{"  SETTLEMENT  ", StatusPaid},
		{"CAPTURE", StatusPaid},
		{"DENY", StatusFailed},
		{"cancel", StatusFailed},
		{"EXPIRE", StatusFailed},
		{"FAILURE", StatusFailed},
		{"PENDING", ""},
		{"CHALLENGE", ""},
		{"settlement-ish", ""},
		{"", ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, classifyWebhookStatus(c.in), "input %q", c.in)
	}
}

// ============================================================
// handleResaleWebhook
// ============================================================

func TestHandleResaleWebhook_PaidUpdatesStatus(t *testing.T) {
	repo := &memObligationRepo{}
	resaleTx := &ResaleTxLite{ID: uuid.New(), Status: "PENDING"}
	rr := &fakeResaleRepo{}
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})
	svc.SetResaleWebhookDeps(rr, nil)

	require.NoError(t, svc.handleResaleWebhook(context.Background(), resaleTx, StatusPaid))
	assert.Equal(t, "COMPLETED", rr.updated)
}

func TestHandleResaleWebhook_FailureLeavesPending(t *testing.T) {
	repo := &memObligationRepo{}
	resaleTx := &ResaleTxLite{ID: uuid.New(), Status: "PENDING"}
	rr := &fakeResaleRepo{}
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})
	svc.SetResaleWebhookDeps(rr, nil)

	require.NoError(t, svc.handleResaleWebhook(context.Background(), resaleTx, StatusFailed))
	assert.Equal(t, 0, rr.updates, "failed resale payments stay PENDING for the expiry worker")
}

func TestHandleResaleWebhook_TransitionalIsNoOp(t *testing.T) {
	repo := &memObligationRepo{}
	resaleTx := &ResaleTxLite{ID: uuid.New(), Status: "PENDING"}
	rr := &fakeResaleRepo{}
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})
	svc.SetResaleWebhookDeps(rr, nil)

	require.NoError(t, svc.handleResaleWebhook(context.Background(), resaleTx, ""))
	assert.Equal(t, 0, rr.updates)
}

func TestSetResaleWebhookDeps_WiresCollaborators(t *testing.T) {
	repo := &memObligationRepo{}
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &sigGateway{})
	rr := &fakeResaleRepo{}

	svc.SetResaleWebhookDeps(rr, nil)

	require.NotNil(t, svc.resaleRepo)
	assert.Equal(t, rr, svc.resaleRepo)
}

// ============================================================
// InitiatePayments: L2 idempotency rebuild + corrupted L1 cache
// ============================================================

func TestInitiatePayments_L2HitRebuildsResponseFromObligations(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	repo := &memObligationRepo{}
	hash := ComputeIdempotencyHash("idem-l2", leader, bid)

	// Seed the L2 row (idempotency_key UNIQUE) without touching L1.
	seedKey := hash
	repo.byKey = map[string]*PaymentObligation{}
	repo.byID = map[uuid.UUID]*PaymentObligation{}
	require.NoError(t, repo.Create(context.Background(), &PaymentObligation{
		ID:             uuid.New(),
		BookingID:      bid,
		UserID:         leader,
		Amount:         "2500.00",
		Status:         StatusPending,
		IdempotencyKey: &seedKey,
	}))

	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	svc, tx, _ := newPayEnv(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	resp, err := svc.InitiatePayments(context.Background(), leader, bid,
		&InitiatePaymentRequest{PaymentMode: ModeBulk, Amount: "2500"}, "idem-l2")

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, bid.String(), resp.BookingID)
	assert.Len(t, resp.Obligations, 1)
	assert.Equal(t, "2500.00", resp.Obligations[0].Amount)
	assert.False(t, tx.tx.committed, "an L2 hit must not re-open a write transaction")
}

func TestInitiatePayments_CorruptedL1CacheFallsThrough(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	repo := &memObligationRepo{}
	svc, tx, idem := newPayEnv(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	// Poison the L1 entry with a non-JSON body.
	hash := ComputeIdempotencyHash("idem-corrupt", leader, bid)
	require.NoError(t, idem.StoreL1(context.Background(), hash, []byte(`{not-json`)))

	resp, err := svc.InitiatePayments(context.Background(), leader, bid,
		&InitiatePaymentRequest{PaymentMode: ModeBulk, Amount: "1000"}, "idem-corrupt")

	require.NoError(t, err, "a corrupted cache entry must fall through to a rebuild")
	require.NotNil(t, resp)
	assert.Len(t, resp.Obligations, 1)
	assert.True(t, tx.tx.committed)
}

func TestInitiatePayments_EmptyIdempotencyKeyIsRejected(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	repo := &memObligationRepo{}
	svc, tx, _ := newPayEnv(t, repo, &fakeBookingReader{}, &fakeGateway{})

	_, err := svc.InitiatePayments(context.Background(), leader, bid,
		&InitiatePaymentRequest{PaymentMode: ModeBulk, Amount: "1000"}, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "idempotency key is required")
	assert.False(t, tx.tx.committed)
}

func TestInitiatePayments_SplitWithZeroMembersIsRejected(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	repo := &memObligationRepo{}
	svc, tx, _ := newPayEnv(t, repo, &fakeBookingReader{}, &fakeGateway{})

	_, err := svc.InitiatePayments(context.Background(), leader, bid,
		&InitiatePaymentRequest{PaymentMode: ModeSplit}, "idem-empty-members")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidMembers)
	assert.False(t, tx.tx.committed)
}

func TestInitiatePayments_SplitWithTooManyMembersIsRejected(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	repo := &memObligationRepo{}
	svc, tx, _ := newPayEnv(t, repo, &fakeBookingReader{}, &fakeGateway{})

	members := make([]InitiateMember, 11)
	for i := range members {
		members[i] = InitiateMember{UserID: uuid.New(), SeatPosition: i + 1, Amount: "100"}
	}

	_, err := svc.InitiatePayments(context.Background(), leader, bid,
		&InitiatePaymentRequest{PaymentMode: ModeSplit, Members: members}, "idem-many-members")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidMembers)
	assert.False(t, tx.tx.committed)
}

func TestInitiatePayments_SplitMemberWithNonPositiveSeatIsRejected(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	repo := &memObligationRepo{}
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	svc, tx, _ := newPayEnv(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	_, err := svc.InitiatePayments(context.Background(), leader, bid,
		&InitiatePaymentRequest{PaymentMode: ModeSplit, Members: []InitiateMember{
			{UserID: uuid.New(), SeatPosition: 0, Amount: "100"},
		}}, "idem-zero-seat")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "seat_position must be > 0")
	assert.False(t, tx.tx.committed)
}

func TestInitiatePayments_SplitMemberWithInvalidAmountIsRejected(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	repo := &memObligationRepo{}
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	svc, tx, _ := newPayEnv(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	_, err := svc.InitiatePayments(context.Background(), leader, bid,
		&InitiatePaymentRequest{PaymentMode: ModeSplit, Members: []InitiateMember{
			{UserID: uuid.New(), SeatPosition: 1, Amount: "-5"},
		}}, "idem-negative-amount")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid member amount")
	assert.False(t, tx.tx.committed)
}

func TestInitiatePayments_BulkWithEmptyAmountIsRejected(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	repo := &memObligationRepo{}
	svc, tx, _ := newPayEnv(t, repo, &fakeBookingReader{}, &fakeGateway{})

	_, err := svc.InitiatePayments(context.Background(), leader, bid,
		&InitiatePaymentRequest{PaymentMode: ModeBulk, Amount: ""}, "idem-empty-amount")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidMembers)
	assert.False(t, tx.tx.committed)
}

func TestInitiatePayments_BulkWithInvalidAmountIsRejected(t *testing.T) {
	bid := uuid.New()
	leader := uuid.New()
	repo := &memObligationRepo{}
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	svc, tx, _ := newPayEnv(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	_, err := svc.InitiatePayments(context.Background(), leader, bid,
		&InitiatePaymentRequest{PaymentMode: ModeBulk, Amount: "abc"}, "idem-bad-amount")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid bulk amount")
	assert.False(t, tx.tx.committed)
}

func TestInitiatePayments_BookingLookupFailurePropagates(t *testing.T) {
	repo := &memObligationRepo{}
	reader := &fakeBookingReader{err: ErrBookingNotFound}
	svc, tx, _ := newPayEnv(t, repo, reader, &fakeGateway{})

	_, err := svc.InitiatePayments(context.Background(), uuid.New(), uuid.New(),
		&InitiatePaymentRequest{PaymentMode: ModeBulk, Amount: "1000"}, "idem-bk-err")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBookingNotFound)
	assert.False(t, tx.tx.committed)
}

func TestInitiatePayments_BeginTxFailurePropagates(t *testing.T) {
	repo := &memObligationRepo{}
	beginner := &fakeBeginner{err: errors.New("pool exhausted")}
	idem := NewIdempotencyService(newTestRedis(t), repo)
	svc := NewPaymentService(beginner, repo, &fakeBookingReader{}, &fakeGateway{}, idem)

	_, err := svc.InitiatePayments(context.Background(), uuid.New(), uuid.New(),
		&InitiatePaymentRequest{PaymentMode: ModeBulk, Amount: "1000"}, "idem-begin-err")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "begin tx")
}
