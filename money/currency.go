package money

import (
	_ "embed"
	"encoding/json"
	"strconv"
	"strings"
)

// RackBill records currency minor units. ISK and UGX retain the historical
// two-digit compatibility units also required by Stripe; changing existing
// account terms by a factor of 100 would require explicit financial migration. All currencies not in this exception
// table use two digits. Provider-specific representations belong in adapters.
// The browser imports this same table; changing it requires contract tests.
//
//go:embed currencies.json
var currencyData []byte
var exponents = func() map[string]int {
	var v map[string]int
	if err := json.Unmarshal(currencyData, &v); err != nil {
		panic(err)
	}
	return v
}()

func Exponent(currency string) int {
	if n, ok := exponents[strings.ToUpper(currency)]; ok {
		return n
	}
	return 2
}

// Decimal never converts through floating point, including for MinInt64.
func Decimal(amount int64, currency string) string {
	digits := strconv.FormatInt(amount, 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	n := Exponent(currency)
	if n == 0 {
		return sign + digits
	}
	if len(digits) <= n {
		digits = strings.Repeat("0", n+1-len(digits)) + digits
	}
	return sign + digits[:len(digits)-n] + "." + digits[len(digits)-n:]
}

func Format(amount int64, currency string) string { return Decimal(amount, currency) + " " + currency }

// Parse accepts a plain signed decimal with no rounding or exponent notation.
func Parse(input, currency string) (int64, error) {
	v := strings.TrimSpace(input)
	sign := ""
	if strings.HasPrefix(v, "-") {
		sign, v = "-", v[1:]
	}
	parts := strings.Split(v, ".")
	if len(parts) > 2 || v == "" || v == "." {
		return 0, ErrRange
	}
	whole, frac := parts[0], ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	n := Exponent(currency)
	if len(frac) > n || (n == 0 && len(parts) > 1) {
		return 0, ErrRange
	}
	for _, c := range whole + frac {
		if c < '0' || c > '9' {
			return 0, ErrRange
		}
	}
	if whole == "" {
		whole = "0"
	}
	amount, err := strconv.ParseInt(sign+whole+frac+strings.Repeat("0", n-len(frac)), 10, 64)
	if err != nil || !Valid(amount) {
		return 0, ErrRange
	}
	return amount, nil
}
