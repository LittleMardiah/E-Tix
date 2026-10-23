package payment

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// ============================================================
// Extra payment tests — P9a-5 (round 2)
// Targets the remaining uncovered error paths in PaymentService
// (initiate / webhook / bulk-assign / resale), PaymentHandler,
// IdempotencyService and the Midtrans adapter transport failures.
// ============================================================

// errObligationRepo injects failures into the in-memory obligation repo.
type errObligationRepo struct {
	*memObligationRepo
	createErr  error
	listErr    error
	idemKeyErr error
	updateErr  error
	activeErr  error
	activeSet  bool
	active     bool
	assignErr  error
	assignSet  bool
	assignN    int64
	onCreate   func()
}

func (r *errObligationRepo) Create(ctx context.Context, o *PaymentObligation) error {
	if r.onCreate != nil {
		r.onCreate()
	}
	if r.createErr != nil {
		return r.createErr
	}
	return r.memObligationRepo.Create(ctx, o)
}

func (r *errObligationRepo) CreateTx(ctx context.Context, _ pgx.Tx, o *PaymentObligation) error {
	return r.Create(ctx, o)
}

func (r *errObligationRepo) ListByBooking(ctx context.Context, id uuid.UUID) ([]PaymentObligation, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.memObligationRepo.ListByBooking(ctx, id)
}

func (r *errObligationRepo) GetByIdempotencyKey(ctx context.Context, key string) (*PaymentObligation, error) {
	if r.idemKeyErr != nil {
		return nil, r.idemKeyErr
	}
	return r.memObligationRepo.GetByIdempotencyKey(ctx, key)
}

func (r *errObligationRepo) UpdateStatusWithWebhook(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	return r.memObligationRepo.UpdateStatusWithWebhook(ctx, tx, id, status)
}

func (r *errObligationRepo) CheckUserActiveForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (bool, error) {
	if r.activeErr != nil {
		return false, r.activeErr
	}
	if r.activeSet {
		return r.active, nil
	}
	return r.memObligationRepo.CheckUserActiveForUpdate(ctx, tx, id)
}

func (r *errObligationRepo) AssignTicketAtomic(ctx context.Context, tx pgx.Tx, ticketID, bookingID, userID uuid.UUID) (int64, error) {
	if r.assignErr != nil {
		return 0, r.assignErr
	}
	if r.assignSet {
		return r.assignN, nil
	}
	return r.memObligationRepo.AssignTicketAtomic(ctx, tx, ticketID, bookingID, userID)
}

// errBookingReader injects per-method failures into the booking reader.
type errBookingReader struct {
	snap       *BookingSnapshot
	getErr     error
	startAtErr error
	startAt    time.Time
}

func (r *errBookingReader) GetByID(_ context.Context, _ uuid.UUID) (*BookingSnapshot, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.snap, nil
}

func (r *errBookingReader) GetByIDForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*BookingSnapshot, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.snap, nil
}

func (r *errBookingReader) GetEventStartAt(_ context.Context, _ uuid.UUID) (time.Time, error) {
	if r.startAtErr != nil {
		return time.Time{}, r.startAtErr
	}
	if !r.startAt.IsZero() {
		return r.startAt, nil
	}
	return time.Now().UTC().Add(24 * time.Hour), nil
}

// errTx is a pgx.Tx whose Commit always fails.
type errTx struct {
	pgx.Tx
	commitErr error
}

func (t *errTx) Commit(context.Context) error   { return t.commitErr }
func (t *errTx) Rollback(context.Context) error { return nil }

// seqBeginner returns a scripted sequence of Begin results.
type seqBeginner struct {
	errs      []error
	commitErr []error
	n         int
}

func (b *seqBeginner) Begin(context.Context) (pgx.Tx, error) {
	i := b.n
	b.n++
	if i < len(b.errs) && b.errs[i] != nil {
		return nil, b.errs[i]
	}
	if i < len(b.commitErr) && b.commitErr[i] != nil {
		return &errTx{commitErr: b.commitErr[i]}, nil
	}
	return &fakeTx{}, nil
}

// errResaleRepo injects a failure into the resale status update.
type errResaleRepo struct {
	*fakeResaleRepo
	updateErr error
}

func (r *errResaleRepo) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	return r.fakeResaleRepo.UpdateStatus(ctx, tx, id, status)
}

// newTestRedisWithServer returns a client plus its miniredis server so a
// test can kill the connection mid-flight.
func newTestRedisWithServer(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, mr
}

// errRedisDown is returned by killableRedis.dial once the simulated outage
// has started.
var errRedisDown = errors.New("payment: simulated redis outage")

// killableRedis drives a go-redis client whose connections can be severed on
// demand, so a test can simulate a Redis outage in the middle of a request.
//
// FIX P9a-12 TD-F9-16: the previous version closed the miniredis server to
// simulate the outage. miniredis binds an OS-assigned ephemeral port and
// mr.Close() releases it back to the OS, so a miniredis started by another
// package running in parallel can re-bind that exact port before the
// assertion runs. The client then talks to the WRONG server and the L1 write
// unexpectedly succeeds -> flaky test.
//
// The fix keeps miniredis alive for the whole test (its port is therefore
// never released and no other process can take it over) and routes every
// dial through a custom dialer that can be killed: after Kill() the dialer
// refuses new connections and drops the pooled ones, which is what a client
// observes when Redis goes away.
type killableRedis struct {
	mu    sync.Mutex
	dead  bool
	conns map[net.Conn]struct{}
}

// newKillableRedis returns a redis client plus its kill switch. The
// miniredis server behind it stays alive until the test ends.
func newKillableRedis(t *testing.T) (*redis.Client, *killableRedis) {
	t.Helper()

	mr := miniredis.RunT(t)
	k := &killableRedis{conns: map[net.Conn]struct{}{}}

	rdb := &redis.Client{Client: goredis.NewClient(&goredis.Options{
		Addr:       mr.Addr(),
		MaxRetries: -1,
		Dialer:     k.dial,
	})}
	t.Cleanup(func() { _ = rdb.Close() })

	return rdb, k
}

// dial opens a connection to the backing server unless the outage started.
func (k *killableRedis) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	k.mu.Lock()
	dead := k.dead
	k.mu.Unlock()
	if dead {
		return nil, errRedisDown
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	k.mu.Lock()
	if k.dead {
		k.mu.Unlock()
		_ = conn.Close()
		return nil, errRedisDown
	}
	k.conns[conn] = struct{}{}
	k.mu.Unlock()

	return conn, nil
}

// Kill starts the simulated outage: pooled connections are dropped and new
// dials are refused. Idempotent.
func (k *killableRedis) Kill() {
	k.mu.Lock()
	k.dead = true
	conns := make([]net.Conn, 0, len(k.conns))
	for conn := range k.conns {
		conns = append(conns, conn)
	}
	k.conns = map[net.Conn]struct{}{}
	k.mu.Unlock()

	for _, conn := range conns {
		_ = conn.Close()
	}
}

// TestKillableRedis_LiveThenKilled documents the contract of the outage
// simulator used by TestInitiatePayments_StoreL1FailureIsNonFatal: the
// server is reachable until Kill() is called, and unreachable afterwards.
//
// FIX P9a-12 TD-F9-16.
func TestKillableRedis_LiveThenKilled(t *testing.T) {
	rdb, kill := newKillableRedis(t)
	ctx := context.Background()

	require.NoError(t, rdb.Set(ctx, "kill-probe", "v", time.Minute).Err())

	kill.Kill()

	require.Error(t, rdb.Set(ctx, "kill-probe", "v2", time.Minute).Err(),
		"commands must fail once the outage started")
	require.Error(t, rdb.Get(ctx, "kill-probe").Err(),
		"commands must fail once the outage started")
}

// newDeadPayRedis points a client at a closed port so every call fails fast.
func newDeadPayRedis(t *testing.T) *redis.Client {
	t.Helper()
	client := &redis.Client{Client: goredis.NewClient(&goredis.Options{
		Addr:        "127.0.0.1:1",
		MaxRetries:  -1,
		DialTimeout: 50 * time.Millisecond,
	})}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// newErrPayEnv wires a PaymentService with fully injectable collaborators.
func newErrPayEnv(t *testing.T, db TxBeginner, repo PaymentObligationRepository, reader BookingReader, gw adapter.PaymentGateway, rdb *redis.Client) *PaymentService {
	t.Helper()
	idem := NewIdempotencyService(rdb, repo)
	return NewPaymentService(db, repo, reader, gw, idem)
}

// splitReq builds a valid split-mode request.
func splitReq(leader uuid.UUID) *InitiatePaymentRequest {
	return &InitiatePaymentRequest{
		PaymentMode: ModeSplit,
		Members: []InitiateMember{
			{UserID: leader, SeatPosition: 1, Amount: "1000.00"},
		},
	}
}

// ============================================================
// Service: InitiatePayments error paths
// ============================================================

func TestInitiatePayments_L1LookupErrorIsFatal(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}}
	svc := newErrPayEnv(t, &fakeBeginner{tx: &fakeTx{}}, repo, &fakeBookingReader{}, &fakeGateway{}, newDeadPayRedis(t))

	_, err := svc.InitiatePayments(context.Background(), uuid.New(), uuid.New(), splitReq(uuid.New()), "idem-l1-dead")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "idempotency L1 lookup")
}

func TestInitiatePayments_L2LookupErrorIsFatal(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}, idemKeyErr: errors.New("idempotency table down")}
	svc := newErrPayEnv(t, &fakeBeginner{tx: &fakeTx{}}, repo, &fakeBookingReader{}, &fakeGateway{}, newTestRedis(t))

	_, err := svc.InitiatePayments(context.Background(), uuid.New(), uuid.New(), splitReq(uuid.New()), "idem-l2-dead")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "idempotency L2 lookup")
}

func TestInitiatePayments_CommitError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}}
	leader := uuid.New()
	snap := &BookingSnapshot{ID: uuid.New(), LeaderUserID: leader, PaymentStatus: StatusPending}
	beginner := &seqBeginner{commitErr: []error{errors.New("commit rejected")}}
	svc := newErrPayEnv(t, beginner, repo, &fakeBookingReader{snap: snap}, &fakeGateway{}, newTestRedis(t))

	_, err := svc.InitiatePayments(context.Background(), leader, snap.ID, splitReq(leader), "idem-commit")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit")
}

func TestInitiatePayments_StoreL1FailureIsNonFatal(t *testing.T) {
	// FIX P9a-12 TD-F9-16: the outage is simulated through the kill switch
	// (see killableRedis) so the miniredis port is never released back to
	// the OS and cannot be re-bound by another package running in parallel.
	rdb, kill := newKillableRedis(t)
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}}
	leader := uuid.New()
	snap := &BookingSnapshot{ID: uuid.New(), LeaderUserID: leader, PaymentStatus: StatusPending}
	// Kill Redis right before the L1 write: the DB already holds the
	// obligations, so the call must still succeed.
	repo.onCreate = func() { kill.Kill() }
	svc := newErrPayEnv(t, &fakeBeginner{tx: &fakeTx{}}, repo, &fakeBookingReader{snap: snap}, &fakeGateway{}, rdb)

	resp, err := svc.InitiatePayments(context.Background(), leader, snap.ID, splitReq(leader), "idem-l1-store")

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Obligations, 1)
}

func TestInitiatePayments_SplitGatewayError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}}
	leader := uuid.New()
	snap := &BookingSnapshot{ID: uuid.New(), LeaderUserID: leader, PaymentStatus: StatusPending}
	gw := &fakeGateway{initiateErr: errors.New("gateway unreachable")}
	svc := newErrPayEnv(t, &fakeBeginner{tx: &fakeTx{}}, repo, &fakeBookingReader{snap: snap}, gw, newTestRedis(t))

	_, err := svc.InitiatePayments(context.Background(), leader, snap.ID, splitReq(leader), "idem-gw-split")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway initiate")
}

func TestInitiatePayments_SplitInsertError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}, createErr: errors.New("insert rejected")}
	leader := uuid.New()
	snap := &BookingSnapshot{ID: uuid.New(), LeaderUserID: leader, PaymentStatus: StatusPending}
	svc := newErrPayEnv(t, &fakeBeginner{tx: &fakeTx{}}, repo, &fakeBookingReader{snap: snap}, &fakeGateway{}, newTestRedis(t))

	_, err := svc.InitiatePayments(context.Background(), leader, snap.ID, splitReq(leader), "idem-ins-split")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert obligation")
}

func TestInitiatePayments_BulkInsertError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}, createErr: errors.New("insert rejected")}
	leader := uuid.New()
	snap := &BookingSnapshot{ID: uuid.New(), LeaderUserID: leader, PaymentStatus: StatusPending}
	svc := newErrPayEnv(t, &fakeBeginner{tx: &fakeTx{}}, repo, &fakeBookingReader{snap: snap}, &fakeGateway{}, newTestRedis(t))

	req := &InitiatePaymentRequest{PaymentMode: ModeBulk, Amount: "1000.00"}
	_, err := svc.InitiatePayments(context.Background(), leader, snap.ID, req, "idem-ins-bulk")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert obligation")
}

func TestInitiatePayments_L2HitListFailure(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}, listErr: errors.New("list failed")}
	leader := uuid.New()
	bid := uuid.New()
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	svc := newErrPayEnv(t, &fakeBeginner{tx: &fakeTx{}}, repo, &fakeBookingReader{snap: snap}, &fakeGateway{}, newTestRedis(t))

	// Seed the L2 row so the lookup hits, then force the follow-up list to fail.
	key := ComputeIdempotencyHash("idem-l2-list", leader, bid)
	seeded := &PaymentObligation{
		ID:             uuid.New(),
		BookingID:      bid,
		UserID:         leader,
		Amount:         "1000.00",
		Status:         StatusPending,
		IdempotencyKey: &key,
	}
	require.NoError(t, repo.memObligationRepo.Create(context.Background(), seeded))

	_, err := svc.InitiatePayments(context.Background(), leader, bid, splitReq(leader), "idem-l2-list")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "list by booking")
}

// ============================================================
// Service: HandleWebhook error paths
// ============================================================

func TestHandleWebhook_BeginTxError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}}
	beginner := &seqBeginner{errs: []error{errors.New("cannot begin")}}
	svc := newErrPayEnv(t, beginner, repo, &fakeBookingReader{}, &sigGateway{}, newTestRedis(t))

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-begin", "SUCCESS"), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "begin tx")
}

func TestHandleWebhook_UpdateStatusError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}, updateErr: errors.New("update rejected")}
	seedObligation(t, repo.memObligationRepo, "tx-upd", StatusPending)
	svc := newErrPayEnv(t, &fakeBeginner{tx: &fakeTx{}}, repo, &fakeBookingReader{}, &sigGateway{}, newTestRedis(t))

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-upd", "SUCCESS"), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "update status")
}

// ============================================================
// Service: BulkAssign error paths
// ============================================================

// bulkReq builds a valid bulk-assign request.
func bulkReq(leader uuid.UUID) *BulkAssignRequest {
	return &BulkAssignRequest{
		BookingID: uuid.New(),
		Assignments: []BulkAssignEntry{
			{TicketID: uuid.New(), UserID: leader},
		},
	}
}

func TestBulkAssign_BeginTxError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}}
	beginner := &seqBeginner{errs: []error{errors.New("cannot begin")}}
	svc := newErrPayEnv(t, beginner, repo, &errBookingReader{}, &fakeGateway{}, newTestRedis(t))

	_, err := svc.BulkAssign(context.Background(), uuid.New(), bulkReq(uuid.New()))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "begin tx")
}

func TestBulkAssign_BookingLookupError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}}
	reader := &errBookingReader{getErr: ErrBookingNotFound}
	svc := newErrPayEnv(t, &fakeBeginner{tx: &fakeTx{}}, repo, reader, &fakeGateway{}, newTestRedis(t))

	_, err := svc.BulkAssign(context.Background(), uuid.New(), bulkReq(uuid.New()))

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBookingNotFound)
}

func TestBulkAssign_EventStartLookupError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}}
	leader := uuid.New()
	reader := &errBookingReader{
		snap:       &BookingSnapshot{ID: uuid.New(), LeaderUserID: leader},
		startAtErr: errors.New("event lookup failed"),
	}
	svc := newErrPayEnv(t, &fakeBeginner{tx: &fakeTx{}}, repo, reader, &fakeGateway{}, newTestRedis(t))

	_, err := svc.BulkAssign(context.Background(), leader, bulkReq(leader))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "event lookup failed")
}

func TestBulkAssign_TargetUserCheckError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}, activeErr: errors.New("user check failed")}
	leader := uuid.New()
	reader := &errBookingReader{snap: &BookingSnapshot{ID: uuid.New(), LeaderUserID: leader}}
	svc := newErrPayEnv(t, &fakeBeginner{tx: &fakeTx{}}, repo, reader, &fakeGateway{}, newTestRedis(t))

	_, err := svc.BulkAssign(context.Background(), leader, bulkReq(leader))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "check user active")
}

func TestBulkAssign_AssignTicketError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}, assignErr: errors.New("assign failed")}
	leader := uuid.New()
	reader := &errBookingReader{snap: &BookingSnapshot{ID: uuid.New(), LeaderUserID: leader}}
	svc := newErrPayEnv(t, &fakeBeginner{tx: &fakeTx{}}, repo, reader, &fakeGateway{}, newTestRedis(t))

	_, err := svc.BulkAssign(context.Background(), leader, bulkReq(leader))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "assign ticket")
}

func TestBulkAssign_CommitError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}}
	leader := uuid.New()
	reader := &errBookingReader{snap: &BookingSnapshot{ID: uuid.New(), LeaderUserID: leader}}
	beginner := &seqBeginner{commitErr: []error{errors.New("commit rejected")}}
	svc := newErrPayEnv(t, beginner, repo, reader, &fakeGateway{}, newTestRedis(t))

	_, err := svc.BulkAssign(context.Background(), leader, bulkReq(leader))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit")
}

// ============================================================
// Service: resale webhook branch error paths
// ============================================================

func TestHandleResaleWebhook_BeginTxError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}}
	resale := &fakeResaleRepo{tx: resaleTxLite("tx-resale-begin")}
	// The webhook's own Begin succeeds; the resale branch's Begin fails.
	beginner := &seqBeginner{errs: []error{nil, errors.New("resale begin failed")}}
	svc := newErrPayEnv(t, beginner, repo, &fakeBookingReader{}, &sigGateway{}, newTestRedis(t))
	svc.SetResaleWebhookDeps(resale, nil)

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-resale-begin", "SUCCESS"), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "resale begin failed")
}

func TestHandleResaleWebhook_UpdateStatusError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}}
	resale := &errResaleRepo{
		fakeResaleRepo: &fakeResaleRepo{tx: resaleTxLite("tx-resale-upd")},
		updateErr:      errors.New("resale update rejected"),
	}
	svc := newErrPayEnv(t, &fakeBeginner{tx: &fakeTx{}}, repo, &fakeBookingReader{}, &sigGateway{}, newTestRedis(t))
	svc.SetResaleWebhookDeps(resale, nil)

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-resale-upd", "SUCCESS"), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "resale update rejected")
}

func TestHandleResaleWebhook_CommitError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}}
	resale := &fakeResaleRepo{tx: resaleTxLite("tx-resale-commit")}
	beginner := &seqBeginner{commitErr: []error{nil, errors.New("resale commit failed")}}
	svc := newErrPayEnv(t, beginner, repo, &fakeBookingReader{}, &sigGateway{}, newTestRedis(t))
	svc.SetResaleWebhookDeps(resale, nil)

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, "tx-resale-commit", "SUCCESS"), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "resale commit failed")
}

// resaleTxLite builds a pending resale transaction for the given tx id.
func resaleTxLite(txID string) *ResaleTxLite {
	r := &ResaleTxLite{
		ID:                   uuid.New(),
		SellerUserID:         uuid.New(),
		SellerPayout:         "5000.00",
		Status:               "PENDING",
		PaymentTransactionID: nil,
	}
	r.PaymentTransactionID = &txID
	return r
}

// ============================================================
// Handler: remaining error-mapping branches
// ============================================================

func TestHandler_GetBookingStatus_InvalidUserID(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	w := payReq(t, r, http.MethodGet, "/bookings/"+uuid.NewString()+"/status",
		"not-a-uuid", "", nil)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"UNAUTHORIZED"`)
}

// bulkAssignBody builds a bulk-assign JSON body.
func bulkAssignBody(t *testing.T, leader uuid.UUID) string {
	t.Helper()
	return `{"booking_id":"` + uuid.NewString() + `","assignments":[{"ticket_id":"` +
		uuid.NewString() + `","user_id":"` + leader.String() + `"}]}`
}

func TestHandler_BulkAssign_BookingNotFoundReturns404(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}}
	reader := &errBookingReader{getErr: ErrBookingNotFound}
	_, r := newPayHandler(t, repo, reader, &fakeGateway{})
	leader := uuid.New()

	w := payReq(t, r, http.MethodPost, "/payments/bulk/assign", leader.String(),
		bulkAssignBody(t, leader), map[string]string{"X-Idempotency-Key": "idem-ba-404"})

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"NOT_FOUND"`)
}

func TestHandler_BulkAssign_EventAlreadyStartedReturns422(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}}
	leader := uuid.New()
	reader := &errBookingReader{
		snap:    &BookingSnapshot{ID: uuid.New(), LeaderUserID: leader},
		startAt: time.Now().UTC().Add(-2 * time.Hour),
	}
	_, r := newPayHandler(t, repo, reader, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/payments/bulk/assign", leader.String(),
		bulkAssignBody(t, leader), map[string]string{"X-Idempotency-Key": "idem-ba-422"})

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"EVENT_ALREADY_STARTED"`)
}

func TestHandler_BulkAssign_TargetUserInactiveReturns422(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}, activeSet: true, active: false}
	leader := uuid.New()
	reader := &errBookingReader{snap: &BookingSnapshot{ID: uuid.New(), LeaderUserID: leader}}
	_, r := newPayHandler(t, repo, reader, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/payments/bulk/assign", leader.String(),
		bulkAssignBody(t, leader), map[string]string{"X-Idempotency-Key": "idem-ba-inactive"})

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"TARGET_USER_INACTIVE_OR_SUSPENDED"`)
}

func TestHandler_BulkAssign_TicketAlreadyAssignedReturns409(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}, assignSet: true, assignN: 0}
	leader := uuid.New()
	reader := &errBookingReader{snap: &BookingSnapshot{ID: uuid.New(), LeaderUserID: leader}}
	_, r := newPayHandler(t, repo, reader, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/payments/bulk/assign", leader.String(),
		bulkAssignBody(t, leader), map[string]string{"X-Idempotency-Key": "idem-ba-409"})

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"CONFLICT"`)
}

func TestHandler_BulkAssign_UnexpectedErrorReturns500(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}, activeErr: errors.New("user check exploded")}
	leader := uuid.New()
	reader := &errBookingReader{snap: &BookingSnapshot{ID: uuid.New(), LeaderUserID: leader}}
	_, r := newPayHandler(t, repo, reader, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/payments/bulk/assign", leader.String(),
		bulkAssignBody(t, leader), map[string]string{"X-Idempotency-Key": "idem-ba-500"})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INTERNAL_ERROR"`)
}

// ============================================================
// IdempotencyService
// ============================================================

func TestIdempotency_LookupL1RedisError(t *testing.T) {
	idem := NewIdempotencyService(newDeadPayRedis(t), &memObligationRepo{})

	_, hit, err := idem.LookupL1(context.Background(), "hash-1")

	require.Error(t, err)
	assert.False(t, hit)
}

func TestIdempotency_LookupL2RepositoryError(t *testing.T) {
	repo := &errObligationRepo{memObligationRepo: &memObligationRepo{}, idemKeyErr: errors.New("db down")}
	idem := NewIdempotencyService(newTestRedis(t), repo)

	_, err := idem.LookupL2(context.Background(), "hash-2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

func TestIdempotency_LookupL2MissReturnsNil(t *testing.T) {
	idem := NewIdempotencyService(newTestRedis(t), &memObligationRepo{})

	o, err := idem.LookupL2(context.Background(), "hash-missing")

	require.NoError(t, err)
	assert.Nil(t, o, "a miss must be (nil, nil) so the caller may proceed")
}

// ============================================================
// Webhook handler: unreadable body
// ============================================================

// errBody is an io.ReadCloser that always fails.
type errBody struct{}

func (errBody) Read([]byte) (int, error) { return 0, errors.New("stream broke") }
func (errBody) Close() error             { return nil }

func TestHandler_HandleWebhook_UnreadableBodyReturns400(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &sigGateway{})

	req := httptest.NewRequest(http.MethodPost, "/webhook/payment", nil)
	req.Body = errBody{}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}
