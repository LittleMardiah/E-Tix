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
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}
	var req ListTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	resp, err := h.svc.ListTicket(c.Request.Context(), uid, &req)
	if err != nil {
		code := 500
		switch err {
		case ErrResaleForbidden:
			code = 403
		case ErrResaleListingExists:
			code = 409
		case ErrResalePriceExceedsCap, ErrResaleTicketNotConfirmed:
			code = 422
		}
		c.JSON(code, gin.H{"error": err.Error()})
		return
	}
	c.JSON(201, gin.H{"data": resp})
}

func (h *ResaleHandler) Browse(c *gin.Context) {
	eid, err := uuid.Parse(c.Query("event_id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid event_id"})
		return
	}
	resp, err := h.svc.Browse(c.Request.Context(), eid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"data": resp})
}

func (h *ResaleHandler) Buy(c *gin.Context) {
	uid, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}
	rid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid resale_id"})
		return
	}
	resp, err := h.svc.BuyResaleTicket(c.Request.Context(), uid, rid)
	if err != nil {
		code := 500
		switch err {
		case ErrResaleNotFound:
			code = 404
		case ErrResaleAlreadyReserved, ErrResaleNotPending:
			code = 409
		case ErrResaleOwnListing:
			code = 422
		}
		c.JSON(code, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"data": resp})
}

func (h *ResaleHandler) Cancel(c *gin.Context) {
	uid, err := uuid.Parse(c.GetString("user_id"))
	if err != nil {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}
	rid, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid resale_id"})
		return
	}
	resp, err := h.svc.Cancel(c.Request.Context(), uid, rid)
	if err != nil {
		code := 500
		switch err {
		case ErrResaleNotFound:
			code = 404
		case ErrResaleForbidden:
			code = 403
		case ErrResaleNotPending:
			code = 422
		}
		c.JSON(code, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"data": resp})
}
