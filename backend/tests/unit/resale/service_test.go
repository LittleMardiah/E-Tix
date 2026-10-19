package resale_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/LittleMardiah/E-Tix/backend/internal/resale"
)

func TestSentinels_Distinct(t *testing.T) {
	s := []error{
		resale.ErrResaleNotFound, resale.ErrResaleForbidden,
		resale.ErrResaleListingExists, resale.ErrResaleNotPending,
		resale.ErrResaleAlreadyReserved, resale.ErrResaleOwnListing,
		resale.ErrResalePriceExceedsCap, resale.ErrResaleTicketNotConfirmed,
	}
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			assert.NotEqual(t, s[i], s[j])
		}
	}
}
