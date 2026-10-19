package resale

import (
	"fmt"

	"github.com/shopspring/decimal"
)

func parseDecimal(s string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, fmt.Errorf("resale: parse decimal %q: %w", s, err)
	}
	return d, nil
}
