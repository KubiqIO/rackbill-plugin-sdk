package money

import (
	"encoding/json"
	"math"
	"math/big"
	"os"
	"testing"
)

func TestCurrencyContract(t *testing.T) {
	data, err := os.ReadFile("corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Currency string
		Minor    int64
		Decimal  string
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		if got := Decimal(tc.Minor, tc.Currency); got != tc.Decimal {
			t.Errorf("%+v: %s", tc, got)
		}
		if got, err := Parse(tc.Decimal, tc.Currency); err != nil || got != tc.Minor {
			t.Errorf("%+v: %d %v", tc, got, err)
		}
	}
	for _, bad := range []string{"NaN", "Inf", "1e3", "1.001", "1.20junk", "90071992547409.92", "-90071992547409.92"} {
		if _, err := Parse(bad, "USD"); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestMoneyBounds(t *testing.T) {
	for _, amount := range []int64{math.MinInt64, math.MaxInt64, Max + 1, -Max - 1} {
		if _, err := Multiply(amount, 1); err == nil {
			t.Errorf("accepted %d", amount)
		}
	}
	if _, err := Multiply((1<<62)+1, 4); err == nil {
		t.Fatal("wrapped line accepted")
	}
	if _, err := Add(Max, 1); err == nil {
		t.Fatal("overflowed sum accepted")
	}
	if got, err := BasisPoints(math.MinInt64, 10000); err != nil || got != math.MinInt64 {
		t.Fatalf("MinInt64: %d %v", got, err)
	}
	if _, err := BasisPoints(math.MaxInt64, 10001); err == nil {
		t.Fatal("out-of-range tax accepted")
	}
}

func TestGatewayCurrencyContract(t *testing.T) {
	for _, tc := range []struct {
		gateway, currency string
		upgrade           bool
	}{
		{"paypal", "HUF", true}, {"paypal", "TWD", true}, {"paypal", "JPY", false},
		{"2checkout", "KWD", true}, {"2checkout", "CLP", true}, {"2checkout", "JPY", false},
		{"stripe", "JPY", false}, {"stripe", "ISK", false},
	} {
		if got := NeedsCurrencyUpgrade(tc.gateway, tc.currency, 0); got != tc.upgrade {
			t.Errorf("%+v: upgrade=%v", tc, got)
		}
		if NeedsCurrencyUpgrade(tc.gateway, tc.currency, 1) {
			t.Errorf("new adapter refused: %+v", tc)
		}
	}
	for _, tc := range []struct {
		gateway, currency string
		amount            int64
		valid             bool
	}{
		{"paypal", "HUF", 1500, true}, {"paypal", "HUF", 1501, false},
		{"stripe", "ISK", 1500, true}, {"stripe", "ISK", 1501, false},
		{"stripe", "JPY", 1501, true}, {"stripe", "KWD", 1501, true},
	} {
		if err := GatewayAmount(tc.gateway, tc.amount, tc.currency); (err == nil) != tc.valid {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}

// The oracle rounds a rational by comparing the remainder with one half;
// it does not reuse the implementation's add-before-divide formula.
func FuzzBasisPoints(f *testing.F) {
	for _, amount := range []int64{0, 1, -1, 4999, 5000, 10001, 1 << 60, math.MinInt64, math.MaxInt64} {
		for _, rate := range []uint16{0, 1, 5000, 10000, 65535} {
			f.Add(amount, rate)
		}
	}
	f.Fuzz(func(t *testing.T, amount int64, rate uint16) {
		rational := new(big.Rat).Mul(new(big.Rat).SetInt64(amount), new(big.Rat).SetFrac64(int64(rate), 10000))
		numerator := new(big.Int).Abs(rational.Num())
		q, rem := new(big.Int), new(big.Int)
		q.QuoRem(numerator, rational.Denom(), rem)
		if rem.Lsh(rem, 1).Cmp(rational.Denom()) >= 0 {
			q.Add(q, big.NewInt(1))
		}
		if amount < 0 {
			q.Neg(q)
		}
		got, err := BasisPoints(amount, int(rate))
		if !q.IsInt64() {
			if err == nil {
				t.Fatal("overflow accepted")
			}
			return
		}
		if err != nil || got != q.Int64() {
			t.Fatalf("%d * %d bps = %d (%v), want %s", amount, rate, got, err, q)
		}
	})
}

func FuzzCurrencyRoundTrip(f *testing.F) {
	f.Add(int64(1500), uint8(0))
	f.Add(Max, uint8(2))
	f.Add(-Max, uint8(3))
	f.Fuzz(func(t *testing.T, amount int64, selector uint8) {
		if !Valid(amount) {
			return
		}
		currency := []string{"USD", "JPY", "KWD", "CLF"}[selector%4]
		got, err := Parse(Decimal(amount, currency), currency)
		if err != nil || got != amount {
			t.Fatalf("round trip: %d %s => %d %v", amount, currency, got, err)
		}
	})
}
