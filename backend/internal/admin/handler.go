package admin

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// FASE 8 P8e — handler skeleton (11 methods).
// Svc methods stubbed in P8g (return "not implemented"). Handler returns 501 now.
// Interface AdminService defined here temporarily; moved to service.go in P8g.

// AdminService = business logic contract (stubs in P8g).
type AdminService interface {
	BulkCancel(ctx context.Context, adminID, eventID uuid.UUID, req *BulkCancelRequest) (*BulkCancelResponse, error)
	GetLedger(ctx context.Context, adminID uuid.UUID, dateFrom, dateTo, status string, page, limit int) (*LedgerResponse, error)
	ManualReverse(ctx context.Context, adminID uuid.UUID, req *LedgerReverseRequest) (*LedgerReverseResponse, error)
	GetStuckLocks(ctx context.Context, adminID uuid.UUID) (*StuckLocksResponse, error)
	ForceRelease(ctx context.Context, adminID uuid.UUID, req *ForceReleaseRequest) (*ForceReleaseResponse, error)
	ListUsers(ctx context.Context, adminID uuid.UUID, role, status, search string, page, limit int) (*UserListResponse, error)
	ListBlacklist(ctx context.Context, adminID uuid.UUID, page, limit int) (*BlacklistListResponse, error)
	AddBlacklist(ctx context.Context, adminID uuid.UUID, req *AddBlacklistRequest) (*AddBlacklistResponse, error)
	RemoveBlacklist(ctx context.Context, adminID, id uuid.UUID) (*RemoveBlacklistResponse, error)
	SuspendUser(ctx context.Context, adminID, userID uuid.UUID, req *SuspendUserRequest) (*SuspendUserResponse, error)
	UnsuspendUser(ctx context.Context, adminID, userID uuid.UUID) (*UnsuspendUserResponse, error)
}

// AdminHandler = HTTP layer.
type AdminHandler struct {
	svc AdminService
}

func NewAdminHandler(svc AdminService) *AdminHandler {
	return &AdminHandler{svc: svc}
}

// helper: parse UUID from path param
func adminParseUUID(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		adminError(c, 400, "INVALID_REQUEST", "invalid "+name)
		return uuid.Nil, false
	}
	return id, true
}

// helper: parse admin ID from context
func adminFromCtx(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		adminError(c, 401, "UNAUTHORIZED", "invalid user context")
		return uuid.Nil, false
	}
	return id, true
}

// ---- E3: Bulk Cancel (§14.1) ----

func (h *AdminHandler) BulkCancel(c *gin.Context) {
	adminID, ok := adminFromCtx(c)
	if !ok {
		return
	}
	eventID, ok := adminParseUUID(c, "event_id")
	if !ok {
		return
	}
	var req BulkCancelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, 400, "INVALID_REQUEST", err.Error())
		return
	}
	resp, err := h.svc.BulkCancel(c.Request.Context(), adminID, eventID, &req)
	if err != nil {
		adminError(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	adminSuccess(c, 202, resp)
}

// ---- E4: Ledger (§14.2) ----

func (h *AdminHandler) GetLedger(c *gin.Context) {
	adminID, ok := adminFromCtx(c)
	if !ok {
		return
	}
	page := 1
	limit := 50
	// TODO(P8g): parse page/limit/date_from/date_to/status from query
	resp, err := h.svc.GetLedger(c.Request.Context(), adminID,
		c.Query("date_from"), c.Query("date_to"), c.Query("status"), page, limit)
	if err != nil {
		adminError(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	adminSuccess(c, 200, resp)
}

// ---- E5: Manual Reverse (§14.3) ----

func (h *AdminHandler) ManualReverse(c *gin.Context) {
	adminID, ok := adminFromCtx(c)
	if !ok {
		return
	}
	var req LedgerReverseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, 400, "INVALID_REQUEST", err.Error())
		return
	}
	resp, err := h.svc.ManualReverse(c.Request.Context(), adminID, &req)
	if err != nil {
		adminError(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	adminSuccess(c, 200, resp)
}

// ---- E6: Stuck Locks (§14.4) ----

func (h *AdminHandler) GetStuckLocks(c *gin.Context) {
	adminID, ok := adminFromCtx(c)
	if !ok {
		return
	}
	resp, err := h.svc.GetStuckLocks(c.Request.Context(), adminID)
	if err != nil {
		adminError(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	adminSuccess(c, 200, resp)
}

// ---- E6: Force Release (§14.5) ----

func (h *AdminHandler) ForceRelease(c *gin.Context) {
	adminID, ok := adminFromCtx(c)
	if !ok {
		return
	}
	var req ForceReleaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, 400, "INVALID_REQUEST", err.Error())
		return
	}
	resp, err := h.svc.ForceRelease(c.Request.Context(), adminID, &req)
	if err != nil {
		adminError(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	adminSuccess(c, 200, resp)
}

// ---- E7: List Users (§14.6) ----

func (h *AdminHandler) ListUsers(c *gin.Context) {
	adminID, ok := adminFromCtx(c)
	if !ok {
		return
	}
	page := 1
	limit := 50
	resp, err := h.svc.ListUsers(c.Request.Context(), adminID,
		c.Query("role"), c.Query("status"), c.Query("search"), page, limit)
	if err != nil {
		adminError(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	adminSuccess(c, 200, resp)
}

// ---- E8: List Blacklist (§14.7) ----

func (h *AdminHandler) ListBlacklist(c *gin.Context) {
	adminID, ok := adminFromCtx(c)
	if !ok {
		return
	}
	resp, err := h.svc.ListBlacklist(c.Request.Context(), adminID, 1, 50)
	if err != nil {
		adminError(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	adminSuccess(c, 200, resp)
}

// ---- E8: Add Blacklist (§14.8) ----

func (h *AdminHandler) AddBlacklist(c *gin.Context) {
	adminID, ok := adminFromCtx(c)
	if !ok {
		return
	}
	var req AddBlacklistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, 400, "INVALID_REQUEST", err.Error())
		return
	}
	resp, err := h.svc.AddBlacklist(c.Request.Context(), adminID, &req)
	if err != nil {
		adminError(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	adminSuccess(c, 201, resp)
}

// ---- E8: Remove Blacklist (§14.9) ----

func (h *AdminHandler) RemoveBlacklist(c *gin.Context) {
	adminID, ok := adminFromCtx(c)
	if !ok {
		return
	}
	id, ok := adminParseUUID(c, "id")
	if !ok {
		return
	}
	resp, err := h.svc.RemoveBlacklist(c.Request.Context(), adminID, id)
	if err != nil {
		adminError(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	adminSuccess(c, 200, resp)
}

// ---- E7: Suspend User (§14.10) ----

func (h *AdminHandler) SuspendUser(c *gin.Context) {
	adminID, ok := adminFromCtx(c)
	if !ok {
		return
	}
	userID, ok := adminParseUUID(c, "user_id")
	if !ok {
		return
	}
	var req SuspendUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, 400, "INVALID_REQUEST", err.Error())
		return
	}
	resp, err := h.svc.SuspendUser(c.Request.Context(), adminID, userID, &req)
	if err != nil {
		adminError(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	adminSuccess(c, 200, resp)
}

// ---- E7: Unsuspend User (§14.11) ----

func (h *AdminHandler) UnsuspendUser(c *gin.Context) {
	adminID, ok := adminFromCtx(c)
	if !ok {
		return
	}
	userID, ok := adminParseUUID(c, "user_id")
	if !ok {
		return
	}
	resp, err := h.svc.UnsuspendUser(c.Request.Context(), adminID, userID)
	if err != nil {
		adminError(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	adminSuccess(c, 200, resp)
}
