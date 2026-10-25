// Package money defines RackBill's integer minor-unit arithmetic contract.
package money

import (
	"errors"
	"math/big"
)

// Max is the largest integer that survives the JSON/browser boundary exactly.
// Bounds apply to inputs, intermediate lines, totals and ledger adjustments.
const Max int64 = 1<<53 - 1
const MaxQuantity = 1<<31 - 1

var ErrRange = errors.New("money amount or quantity is outside the supported range")

func Valid(n int64) bool { return n >= -Max && n <= Max }

func Add(a, b int64) (int64, error) {
	if !Valid(a) || !Valid(b) {
		return 0, ErrRange
	}
	n := a + b // two bounded inputs cannot overflow int64
	if !Valid(n) {
		return 0, ErrRange
	}
	return n, nil
}

func Multiply(unit int64, quantity int) (int64, error) {
	if !Valid(unit) || quantity <= 0 || quantity > MaxQuantity {
		return 0, ErrRange
	}
	if unit > Max/int64(quantity) || unit < -Max/int64(quantity) {
		return 0, ErrRange
	}
	return unit * int64(quantity), nil
}

// BasisPoints rounds halves away from zero without overflowing intermediates,
// including MinInt64. Its result must fit int64; callers additionally enforce
// the transport bound on persisted amounts. The independent tests use a
// rational oracle and exercise the full int64 domain.
func BasisPoints(amount int64, rate int) (int64, error) {
	return Ratio(amount, int64(rate), 10000)
}

// Ratio is exact integer multiplication/division, rounded half away from zero.
func Ratio(amount, numerator, denominator int64) (int64, error) {
	if numerator < 0 || denominator <= 0 {
		return 0, ErrRange
	}
	n := new(big.Int).Mul(big.NewInt(amount), big.NewInt(numerator))
	negative := n.Sign() < 0
	n.Abs(n).Add(n, big.NewInt(denominator/2)).Quo(n, big.NewInt(denominator))
	if negative {
		n.Neg(n)
	}
	if !n.IsInt64() {
		return 0, ErrRange
	}
	return n.Int64(), nil
}
