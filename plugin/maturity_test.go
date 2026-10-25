package plugin

import "testing"

func TestNormalizeMaturity(t *testing.T) {
	cases := map[string]string{
		"":        MaturityAlpha, // undeclared: the floor, never an implied stable
		"alpha":   MaturityAlpha,
		"beta":    MaturityBeta,
		" Beta ":  MaturityBeta,
		"STABLE":  MaturityStable,
		"dev":     MaturityDev,
		"ga":      MaturityAlpha, // unrecognized folds to alpha
		"release": MaturityAlpha,
	}
	for in, want := range cases {
		if got := NormalizeMaturity(in); got != want {
			t.Errorf("NormalizeMaturity(%q) = %q, want %q", in, got, want)
		}
	}
	if len(Maturities) != 4 || Maturities[0] != MaturityDev || Maturities[3] != MaturityStable {
		t.Fatalf("Maturities = %v", Maturities)
	}
}
