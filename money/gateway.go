package money

import (
	"errors"
	"strings"
)

var ErrGatewayPrecision = errors.New("payment gateway cannot represent this amount in the selected currency")

// NeedsCurrencyUpgrade prevents loading an older adapter from silently
// reinstating its previous, incompatible amount scale.
func NeedsCurrencyUpgrade(gateway, currency string, version int32) bool {
	if version >= 1 {
		return false
	}
	currency = strings.ToUpper(currency)
	return gateway == "paypal" && (currency == "HUF" || currency == "TWD") ||
		gateway == "2checkout" && Exponent(currency) != 2 && currency != "JPY" && currency != "KRW" && currency != "VND"
}

// GatewayAmount checks known first-party currency restrictions before a
// durable attempt or refund reservation is created. Adapters also validate
// these limits for callers using the SDK directly.
func GatewayAmount(gateway string, amount int64, currency string) error {
	if !Valid(amount) {
		return ErrRange
	}
	currency = strings.ToUpper(currency)
	whole := gateway == "stripe" && (currency == "ISK" || currency == "UGX") ||
		gateway == "paypal" && (currency == "HUF" || currency == "TWD")
	if whole && amount%100 != 0 {
		return ErrGatewayPrecision
	}
	return nil
}
