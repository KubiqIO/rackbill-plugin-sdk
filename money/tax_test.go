package money

import (
	"encoding/json"
	"math"
	"testing"
)

func TestFractionalTaxArithmetic(t *testing.T) {
	for _, tc := range []struct {
		percent      string
		amount, want int64
	}{
		{"9.725", 10000, 973}, // exactly 972.5 minor units: half away from zero
		{"9.725", -10000, -973},
		{"9.725", 9999, 972},
		{"9.975", 100000, 9975},
		{"0.000001", 50000000, 1},
		{"100", Max, Max},
		{"9.725", Max, 875950127523561},
	} {
		rate, err := ParseTaxPercent(tc.percent)
		if err != nil {
			t.Fatal(err)
		}
		got, err := rate.Apply(tc.amount)
		if err != nil || got != tc.want {
			t.Errorf("%s%% of %d = %d, %v; want %d", tc.percent, tc.amount, got, err, tc.want)
		}
	}
}

func TestTaxRatePrecisionAndJSON(t *testing.T) {
	for _, input := range []string{"972.5", "997.5", "0.0001", "1234.5678", "0", "10000"} {
		var rate TaxRate
		if err := json.Unmarshal([]byte(input), &rate); err != nil {
			t.Fatal(input, err)
		}
		b, err := json.Marshal(rate)
		if err != nil || string(b) != input {
			t.Fatalf("round trip %s: %s %v", input, b, err)
		}
		value, err := rate.Value()
		var scanned TaxRate
		if err != nil {
			t.Fatal(err)
		}
		if err := scanned.Scan(value); err != nil || scanned != rate {
			t.Fatal(value, scanned, err)
		}
		fromPercent, err := ParseTaxPercent(rate.Percent())
		if err != nil || fromPercent != rate {
			t.Fatal(rate, fromPercent, err)
		}
	}
	for _, input := range []string{"null", `"972.5"`, "-1", "10000.0001", "972.50001", "972.50000000000001", "1e100000", "NaN"} {
		var rate TaxRate
		if err := json.Unmarshal([]byte(input), &rate); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
	for _, rate := range []TaxRate{-1, 10001, 972.50001, TaxRate(math.NaN()), TaxRate(math.Inf(1))} {
		if _, err := rate.Apply(10000); err == nil {
			t.Errorf("calculated invalid rate %v", rate)
		}
	}
}
