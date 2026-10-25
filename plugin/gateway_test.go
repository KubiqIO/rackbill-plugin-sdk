package plugin

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"kubiq.io/rackbill/sdk/plugin/proto"
)

type refundGateway struct {
	PaymentGateway
	got         RefundInput
	checkInput  CheckChargeInput
	chargeInput ChargeInput
}

func (g *refundGateway) Describe(context.Context) (GatewayInfo, error) {
	return GatewayInfo{Name: "test", SupportsRefunds: true, SupportsRefundChecks: true, SupportsRecurringPayments: true, RecurringPaymentsConfigKey: "vault_enabled", CurrencyUnitsVersion: 1, IdempotencyWindowSeconds: 86400}, nil
}

func (g *refundGateway) Charge(_ context.Context, in ChargeInput) (ChargeResult, error) {
	g.chargeInput = in
	return ChargeResult{Status: ChargePending, TransactionID: "pi_recurring"}, nil
}
func (g *refundGateway) Refund(_ context.Context, in RefundInput) (RefundResult, error) {
	g.got = in
	return RefundResult{Status: RefundPending, RefundID: "re_1"}, nil
}
func (g *refundGateway) CheckRefund(_ context.Context, in CheckRefundInput) (RefundResult, error) {
	return RefundResult{Status: RefundRequiresAction, RefundID: in.RefundID, FailureReason: in.Config["reason"]}, nil
}

func (g *refundGateway) CheckCharge(_ context.Context, in CheckChargeInput) (CheckChargeResult, error) {
	g.checkInput = in
	return CheckChargeResult{Status: ChargeSucceeded, TransactionID: "pi_verified", AmountCents: 1500, Currency: "JPY", PaymentToken: "provider-private-token"}, nil
}

func TestGatewayRefundExtensionRoundTrip(t *testing.T) {
	impl := &refundGateway{}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	proto.RegisterPaymentGatewayServer(srv, GatewayServer(impl))
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///buf", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	gw := GatewayClient(proto.NewPaymentGatewayClient(conn))
	info, err := gw.Describe(t.Context())
	if err != nil || !info.SupportsRefundChecks || !info.SupportsRecurringPayments || info.IdempotencyWindowSeconds != 86400 || info.CurrencyUnitsVersion != 1 {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	if info.RecurringPaymentsConfigKey != "vault_enabled" || info.RecurringPaymentsEnabled(nil) || !info.RecurringPaymentsEnabled(map[string]string{"vault_enabled": "1"}) || !info.RecurringPaymentsEnabled(map[string]string{"vault_enabled": "true"}) {
		t.Fatal("merchant vault prerequisite lost over RPC")
	}
	res, err := gw.Refund(t.Context(), RefundInput{TransactionID: "pi_1", AmountCents: 500, Currency: "USD", IdempotencyKey: "stable-key", Config: map[string]string{"key": "secret"}})
	if err != nil || res.Status != RefundPending || impl.got.IdempotencyKey != "stable-key" || impl.got.Config["key"] != "secret" {
		t.Fatalf("refund=%+v input=%+v err=%v", res, impl.got, err)
	}
	res, err = gw.(RefundChecker).CheckRefund(t.Context(), CheckRefundInput{RefundID: "re_1", Config: map[string]string{"reason": "needs customer action"}})
	if err != nil || res.Status != RefundRequiresAction || res.FailureReason != "needs customer action" {
		t.Fatalf("check=%+v err=%v", res, err)
	}
	charge, err := gw.CheckCharge(t.Context(), CheckChargeInput{TransactionID: "cs_verified", ExpectedAmountCents: 1500, ExpectedCurrency: "JPY", Config: map[string]string{"tenant": "merchant"}, RetrievePaymentMethod: true})
	if err != nil || charge.AmountCents != 1500 || charge.Currency != "JPY" || impl.checkInput.ExpectedAmountCents != 1500 || impl.checkInput.ExpectedCurrency != "JPY" || impl.checkInput.Config["tenant"] != "merchant" {
		t.Fatalf("charge evidence lost over RPC: %+v %+v %v", charge, impl.checkInput, err)
	}
	if !impl.checkInput.RetrievePaymentMethod || charge.PaymentToken != "provider-private-token" {
		t.Fatal("vault retrieval lost over RPC")
	}
	_, err = gw.Charge(t.Context(), ChargeInput{SavePaymentMethod: true})
	if err != nil || !impl.chargeInput.SavePaymentMethod {
		t.Fatalf("consent lost over RPC: %v", err)
	}
	_, err = gw.Charge(t.Context(), ChargeInput{PaymentToken: "provider-private-token"})
	if err != nil || impl.chargeInput.PaymentToken != "provider-private-token" {
		t.Fatalf("off-session token lost over RPC: %v", err)
	}

}

func TestPaymentTokensNeverMarshalToJSON(t *testing.T) {
	for _, value := range []any{ChargeInput{PaymentToken: "provider-private-token"}, CheckChargeResult{PaymentToken: "provider-private-token"}} {
		data, err := json.Marshal(value)
		if err != nil || strings.Contains(string(data), "provider-private-token") || strings.Contains(string(data), "PaymentToken") {
			t.Fatal("private payment token reached JSON")
		}
	}
}

type legacyGateway struct{ PaymentGateway }

func TestLegacyGatewayRefundCheckIsUnimplemented(t *testing.T) {
	_, err := GatewayServer(legacyGateway{}).CheckRefund(t.Context(), &proto.CheckRefundRequest{RefundId: "re_old"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("legacy extension error=%v", err)
	}
}
