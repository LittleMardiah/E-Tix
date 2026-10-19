package gate

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// TestHandler_SyncOfflineScans_BodyTooLarge verifies B3 fix:
// MaxBytesReader rejects >1MB body BEFORE service call.
func TestHandler_SyncOfflineScans_BodyTooLarge(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Valid JSON whose payload exceeds the 1MB limit — decode must hit
	// MaxBytesReader while reading, not fail on a syntax error first.
	payload := `{"device_id":"` + uDevice.String() + `","pad":"` +
		strings.Repeat("a", 2*1024*1024) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/gate/sync",
		bytes.NewReader([]byte(payload)))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set("user_id", uuid.New().String())

	// svc nil is safe: body limit fires in ShouldBindJSON before service call.
	h := NewGateHandler(nil)
	h.SyncOfflineScans(c)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code,
		"body >1MB must return 413 (B3)")
}
