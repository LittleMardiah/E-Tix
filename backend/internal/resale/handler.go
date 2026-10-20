package resale

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ResaleHandler struct {
	svc *ResaleService
}

func NewResaleHandler(svc *ResaleService) *ResaleHandler {
	return &ResaleHandler{svc: svc}
}

func (h *ResaleHandler) List(c *gin.Context) {
	uid, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		resaleError(c, 401, "UNAUTHORIZED", "invalid user context")
		return
	}
	var req ListTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resaleError(c, 400, "INVALID_REQUEST", err.Error())
		return
	}
	resp, err := h.svc.ListTicket(c.Request.Context(), uid, &req)
	if err != nil {
		switch err {
		case ErrResaleNotFound:
			resaleError(c, 404, "NOT_FOUND", "ticket not found")
		case ErrResaleForbidden:
			resaleError(c, 403, "FORBIDDEN", "not ticket owner")
		case ErrResaleListingExists:
			resaleError(c, 409, "RESALE_LISTING_EXISTS", "ticket already has active listing")
		case ErrResalePriceExceedsCap:
			resaleError(c, 422, "UNPROCESSABLE_ENTITY", "price exceeds cap")
		case ErrResaleTicketNotConfirmed:
			resaleError(c, 422, "UNPROCESSABLE_ENTITY", "ticket not confirmed")
		default:
			resaleError(c, 500, "INTERNAL_ERROR", err.Error())
		}
		return
	}
	resaleSuccess(c, 201, resp)
}

func (h *ResaleHandler) Browse(c *gin.Context) {
	eid, err := uuid.Parse(c.Query("event_id"))
	if err != nil {
		resaleError(c, 400, "INVALID_REQUEST", "invalid event_id")
		return
	}
	resp, err := h.svc.Browse(c.Request.Context(), eid)
	if err != nil {
		resaleError(c, 500, "INTERNAL_ERROR", err.Error())
		return
	}
	resaleSuccess(c, 200, resp)
}

func (h *ResaleHandler) Buy(c *gin.Context) {
	uid, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		resaleError(c, 401, "UNAUTHORIZED", "invalid user context")
		return
	}
	rid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		resaleError(c, 400, "INVALID_REQUEST", "invalid resale_id")
		return
	}
	resp, err := h.svc.BuyResaleTicket(c.Request.Context(), uid, rid)
	if err != nil {
		switch err {
		case ErrResaleNotFound:
			resaleError(c, 404, "NOT_FOUND", "resale not found")
		case ErrResaleAlreadyReserved:
			resaleError(c, 409, "CONFLICT", "already reserved")
		case ErrResaleNotPending:
			resaleError(c, 409, "CONFLICT", "not pending")
		case ErrResaleOwnListing:
			resaleError(c, 422, "UNPROCESSABLE_ENTITY", "cannot buy own listing")
		default:
			resaleError(c, 500, "INTERNAL_ERROR", err.Error())
		}
		return
	}
	resaleSuccess(c, 200, resp)
}

func (h *ResaleHandler) Cancel(c *gin.Context) {
	uid, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		resaleError(c, 401, "UNAUTHORIZED", "invalid user context")
		return
	}
	rid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		resaleError(c, 400, "INVALID_REQUEST", "invalid resale_id")
		return
	}
	resp, err := h.svc.Cancel(c.Request.Context(), uid, rid)
	if err != nil {
		switch err {
		case ErrResaleNotFound:
			resaleError(c, 404, "NOT_FOUND", "resale not found")
		case ErrResaleForbidden:
			resaleError(c, 403, "FORBIDDEN", "not seller")
		case ErrResaleNotPending:
			resaleError(c, 422, "UNPROCESSABLE_ENTITY", "cannot cancel: already reserved or not pending")
		default:
			resaleError(c, 500, "INTERNAL_ERROR", err.Error())
		}
		return
	}
	resaleSuccess(c, 200, resp)
}
