package ledger

import (
	"fmt"
	"math/big"
	"strings"
)

// ConvertToUSD converts minor units of currency cur into USD cents using a decimal rate
// string meaning "USD per one whole unit of cur" (as stored in the prices table), rounding
// half away from zero. USD itself needs no rate. Exact arithmetic: no floats.
func ConvertToUSD(minor int64, cur, rate string) (int64, error) {
	if strings.EqualFold(cur, "USD") {
		return minor, nil
	}
	r, ok := new(big.Rat).SetString(strings.TrimSpace(rate))
	if !ok || r.Sign() <= 0 {
		return 0, fmt.Errorf("invalid rate %q for %s", rate, cur)
	}
	// usd cents = minor / 10^dec(cur) * rate * 10^2
	v := new(big.Rat).SetInt64(minor)
	v.Mul(v, r)
	v.Mul(v, new(big.Rat).SetInt(pow10Int(Decimals("USD"))))
	v.Quo(v, new(big.Rat).SetInt(pow10Int(Decimals(cur))))
	return roundHalfAwayFromZero(v), nil
}

func pow10Int(n int) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }

func roundHalfAwayFromZero(v *big.Rat) int64 {
	neg := v.Sign() < 0
	abs := new(big.Rat).Abs(v)
	abs.Add(abs, big.NewRat(1, 2))
	n := new(big.Int).Quo(abs.Num(), abs.Denom()) // floor for non-negative values
	out := n.Int64()
	if neg {
		return -out
	}
	return out
}
