package money

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
)

// TaxRate is a decimal number of basis points (972.5 means 9.725%).
// Its numeric representation is for transport and comparison only. All tax
// arithmetic uses Units, an exact integer count of millionths of a percent.
// Rates range from 0 to 100%, with at most six decimal places in percent.
type TaxRate float64

const TaxRateScale int64 = 10000 // units per basis point
const TaxRateDenominator int64 = 10000 * TaxRateScale

var ErrTaxRate = errors.New("tax rate must be between 0 and 100 percent with at most six decimal places")

func parseTaxUnits(s string, scale int64) (int64, error) {
	if len(s) == 0 || len(s) > 64 || strings.ContainsAny(s, "/eE") {
		return 0, ErrTaxRate
	}
	digits, dots := 0, 0
	for _, c := range s {
		if c == '.' {
			dots++
		} else if c >= '0' && c <= '9' {
			digits++
		} else {
			return 0, ErrTaxRate
		}
	}
	if digits == 0 || dots > 1 {
		return 0, ErrTaxRate
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || r.Sign() < 0 {
		return 0, ErrTaxRate
	}
	r.Mul(r, new(big.Rat).SetInt64(scale))
	if !r.IsInt() || !r.Num().IsInt64() || r.Num().Int64() > TaxRateDenominator {
		return 0, ErrTaxRate
	}
	return r.Num().Int64(), nil
}

// ParseTaxPercent parses a percentage without rounding or money-currency rules.
func ParseTaxPercent(s string) (TaxRate, error) {
	u, err := parseTaxUnits(strings.TrimSpace(s), 1000000)
	return TaxRate(float64(u) / float64(TaxRateScale)), err
}

func (r TaxRate) Units() (int64, error) {
	return parseTaxUnits(strconv.FormatFloat(float64(r), 'f', -1, 64), TaxRateScale)
}

func (r TaxRate) Apply(amount int64) (int64, error) {
	u, err := r.Units()
	if err != nil {
		return 0, err
	}
	return Ratio(amount, u, TaxRateDenominator)
}

// Percent preserves every supported decimal digit, with at least two places.
func (r TaxRate) Percent() string {
	u, err := r.Units()
	if err != nil {
		return ""
	}
	fraction := strconv.FormatInt(u%1000000+1000000, 10)[1:]
	fraction = strings.TrimRight(fraction, "0")
	for len(fraction) < 2 {
		fraction += "0"
	}
	return strconv.FormatInt(u/1000000, 10) + "." + fraction
}

func (r *TaxRate) UnmarshalJSON(b []byte) error {
	// Decode the original decimal before float conversion can erase extra digits.
	if !json.Valid(b) || string(b) == "null" {
		return ErrTaxRate
	}
	u, err := parseTaxUnits(string(b), TaxRateScale)
	if err != nil {
		return err
	}
	*r = TaxRate(float64(u) / float64(TaxRateScale))
	return nil
}

// Value and Scan keep PostgreSQL NUMERIC values decimal across the driver.
func (r TaxRate) Value() (driver.Value, error) {
	if _, err := r.Units(); err != nil {
		return nil, err
	}
	return strconv.FormatFloat(float64(r), 'f', -1, 64), nil
}

func (r *TaxRate) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	case int64: // old integer columns during upgrades
		s = strconv.FormatInt(v, 10)
	default:
		return ErrTaxRate
	}
	u, err := parseTaxUnits(s, TaxRateScale)
	if err == nil {
		*r = TaxRate(float64(u) / float64(TaxRateScale))
	}
	return err
}
