package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComputeLoyaltyTierP9a8(t *testing.T) {
	cases := []struct {
		name      string
		base      int
		purchases int
		want      string
	}{
		{name: "zero", base: 0, purchases: 0, want: "TIER_3"},
		{name: "tier3 upper bound", base: 69, purchases: 0, want: "TIER_3"},
		{name: "tier2 lower bound", base: 70, purchases: 0, want: "TIER_2"},
		{name: "tier2 upper bound", base: 89, purchases: 0, want: "TIER_2"},
		{name: "tier1 lower bound", base: 90, purchases: 0, want: "TIER_1"},
		{name: "purchases lift tier", base: 50, purchases: 2, want: "TIER_2"},
		{name: "purchases reach tier1", base: 40, purchases: 5, want: "TIER_1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, computeLoyaltyTier(tc.base, tc.purchases))
		})
	}
}
