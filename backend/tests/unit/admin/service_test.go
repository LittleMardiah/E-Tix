package admin_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/LittleMardiah/E-Tix/backend/internal/admin"
)

func TestSentinels_Distinct(t *testing.T) {
	s := []error{
		admin.ErrAdminNotFound, admin.ErrAdminForbidden, admin.ErrAdminConflict,
		admin.ErrAdminUnprocessable, admin.ErrAdmin2FARequired, admin.ErrAdmin2FAInvalid,
		admin.ErrAdmin2FALocked, admin.ErrAdmin2FANotEnrolled,
	}
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			assert.NotEqual(t, s[i], s[j])
		}
	}
}
