package plugin

import "strings"

// Maturity levels a plugin declares in Describe (`maturity`). The agent shows
// the level wherever a plugin is offered — Admin → Plugins, every gateway and
// provider picker — so an operator enabling an integration knows how far
// along it is. Levels are strings, not enums (decision 4), and the set is
// closed: NormalizeMaturity folds anything else onto it.
const (
	// MaturityDev marks a contract simulator: it fakes the remote system and
	// never moves money or touches real infrastructure. The host assigns it
	// to every `<kind>-dev` binary by name — simulators need not declare it.
	MaturityDev = "dev"
	// MaturityAlpha is a real integration early in its life: the contract is
	// implemented; coverage of the remote API and field experience are thin.
	// Also what an undeclared maturity normalizes to.
	MaturityAlpha = "alpha"
	// MaturityBeta is feature-complete for its kind and in use, still
	// accumulating production mileage.
	MaturityBeta = "beta"
	// MaturityStable is production-proven.
	MaturityStable = "stable"
)

// Maturities lists the levels in ascending order.
var Maturities = []string{MaturityDev, MaturityAlpha, MaturityBeta, MaturityStable}

// NormalizeMaturity maps a declared maturity onto the closed set. The empty
// string (a plugin predating the field) and anything unrecognized become
// alpha: an unstated maturity is the least mature real integration, never an
// implied stable. Matching is case-insensitive and ignores surrounding space.
func NormalizeMaturity(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case MaturityDev:
		return MaturityDev
	case MaturityBeta:
		return MaturityBeta
	case MaturityStable:
		return MaturityStable
	default:
		return MaturityAlpha
	}
}
