package plugin

import (
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"kubiq.io/rackbill/sdk/money"
	"kubiq.io/rackbill/sdk/plugin/proto"
)

type taxAccounting struct {
	AccountingProvider
	taxes []AccountingInvoiceTax
}

func (*taxAccounting) Describe(context.Context) (AccountingInfo, error) {
	return AccountingInfo{Name: "tax"}, nil
}
func (p *taxAccounting) SyncInvoice(_ context.Context, in SyncInvoiceInput) (SyncResult, error) {
	p.taxes = in.Taxes
	return SyncResult{Status: SyncSynced}, nil
}

// A pre-upgrade server neither advertises nor reads the additive rate field.
type oldTaxServer struct{ *accountingGRPCServer }

func (*oldTaxServer) DescribeAccounting(context.Context, *proto.DescribeAccountingRequest) (*proto.DescribeAccountingResponse, error) {
	return &proto.DescribeAccountingResponse{Name: "old"}, nil
}

func TestAccountingFractionalTaxTransport(t *testing.T) {
	for _, old := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "legacy"}[old], func(t *testing.T) {
			impl := &taxAccounting{}
			server := &accountingGRPCServer{impl: impl}
			lis := bufconn.Listen(1 << 20)
			srv := grpc.NewServer()
			if old {
				proto.RegisterAccountingServer(srv, &oldTaxServer{server})
			} else {
				proto.RegisterAccountingServer(srv, server)
			}
			go srv.Serve(lis)
			t.Cleanup(srv.Stop)
			conn, err := grpc.NewClient("passthrough:///buf", grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close() })
			wire := proto.NewAccountingClient(conn)
			client := &accountingGRPCClient{client: wire}
			for _, rate := range []money.TaxRate{825, 972.5, 997.5, 1234.5678, 0.0001} {
				impl.taxes = nil
				_, err := client.SyncInvoice(t.Context(), SyncInvoiceInput{Taxes: []AccountingInvoiceTax{{RateBps: rate, AmountCents: 973}}})
				if old && rate != 825 {
					if err == nil || !strings.Contains(err.Error(), "upgraded") || impl.taxes != nil {
						t.Fatalf("legacy call sent fractional rate: %+v %v", impl.taxes, err)
					}
					continue
				}
				if err != nil || len(impl.taxes) != 1 || impl.taxes[0].RateBps != rate || impl.taxes[0].AmountCents != 973 {
					t.Fatalf("transport=%+v %v", impl.taxes, err)
				}
			}
			// Old agents only send integer basis points; updated plugins retain them.
			_, err = wire.SyncInvoice(t.Context(), &proto.SyncInvoiceRequest{Taxes: []*proto.AccountingInvoiceTax{{RateBps: 825}}})
			if err != nil || impl.taxes[0].RateBps != 825 {
				t.Fatalf("legacy host: %+v %v", impl.taxes, err)
			}
		})
	}
}

type mappingAccounting struct {
	AccountingProvider
	lines   []AccountingInvoiceLine
	refunds []AccountingRefundLine
}

func (*mappingAccounting) Describe(context.Context) (AccountingInfo, error) {
	return AccountingInfo{Name: "mapping"}, nil
}
func (p *mappingAccounting) SyncInvoice(_ context.Context, in SyncInvoiceInput) (SyncResult, error) {
	p.lines = in.Lines
	return SyncResult{Status: SyncSynced}, nil
}
func (p *mappingAccounting) SyncRefund(_ context.Context, in SyncRefundInput) (SyncResult, error) {
	p.refunds = in.Lines
	return SyncResult{Status: SyncSynced}, nil
}

type listingAccounting struct{ mappingAccounting }

func (*listingAccounting) ListAccountingOptions(_ context.Context, tenantID, key string, config map[string]string) (AccountingOptions, error) {
	if key == "broken" {
		return AccountingOptions{FailureReason: "listing refused", ConfigUpdates: map[string]string{"refresh_token": "rotated"}}, nil
	}
	return AccountingOptions{
		Options:       []ConfigOption{{Value: "57", Label: key + " " + tenantID + " " + config["realm_id"], Description: "Income"}},
		ConfigUpdates: map[string]string{"refresh_token": "rotated"},
	}, nil
}

// A plugin built against the plain lister still answers, without rotations.
type plainListingAccounting struct{ mappingAccounting }

func (*plainListingAccounting) ListConfigOptions(context.Context, string, string, map[string]string) ([]ConfigOption, error) {
	return []ConfigOption{{Value: "1", Label: "One"}}, nil
}

func accountingClient(t *testing.T, impl AccountingProvider) *accountingGRPCClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	proto.RegisterAccountingServer(srv, &accountingGRPCServer{impl: impl})
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///buf", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &accountingGRPCClient{client: proto.NewAccountingClient(conn)}
}

// Contract 15: line mappings and refund allocations cross the wire intact,
// and option enumeration is optional.
func TestAccountingProductMappingTransport(t *testing.T) {
	impl := &listingAccounting{}
	client := accountingClient(t, impl)
	mapping := map[string]string{"revenue_account": "57"}
	if _, err := client.SyncInvoice(t.Context(), SyncInvoiceInput{Lines: []AccountingInvoiceLine{{AmountCents: 100, Config: mapping}, {AmountCents: 50}}}); err != nil {
		t.Fatal(err)
	}
	if len(impl.lines) != 2 || impl.lines[0].Config["revenue_account"] != "57" || len(impl.lines[1].Config) != 0 {
		t.Fatalf("invoice lines = %+v", impl.lines)
	}
	if _, err := client.SyncRefund(t.Context(), SyncRefundInput{AmountCents: 150, Lines: []AccountingRefundLine{{AmountCents: 100, Config: mapping}, {AmountCents: 50}}}); err != nil {
		t.Fatal(err)
	}
	if len(impl.refunds) != 2 || impl.refunds[0].AmountCents != 100 || impl.refunds[0].Config["revenue_account"] != "57" || impl.refunds[1].AmountCents != 50 {
		t.Fatalf("refund lines = %+v", impl.refunds)
	}
	res, err := client.ListAccountingOptions(t.Context(), "t1", "revenue_account", map[string]string{"realm_id": "r9"})
	if err != nil || len(res.Options) != 1 || res.Options[0].Value != "57" || res.Options[0].Label != "revenue_account t1 r9" || res.Options[0].Description != "Income" || res.ConfigUpdates["refresh_token"] != "rotated" {
		t.Fatalf("options = %+v %v", res, err)
	}
	if res, err := client.ListAccountingOptions(t.Context(), "t1", "broken", nil); err != nil || res.FailureReason != "listing refused" || res.ConfigUpdates["refresh_token"] != "rotated" || len(res.Options) != 0 {
		t.Fatalf("declared failure = %+v %v", res, err)
	}
	if res, err := accountingClient(t, &plainListingAccounting{}).ListAccountingOptions(t.Context(), "t1", "k", nil); err != nil || len(res.Options) != 1 || len(res.ConfigUpdates) != 0 {
		t.Fatalf("plain lister = %+v %v", res, err)
	}
	if _, err := accountingClient(t, &mappingAccounting{}).ListAccountingOptions(t.Context(), "t1", "revenue_account", nil); status.Code(err) != codes.Unimplemented {
		t.Fatalf("plugin without a lister: %v", err)
	}
}

func TestAccountingInfoSplitsProductFields(t *testing.T) {
	info := AccountingInfo{ConfigFields: []ConfigField{{Key: "realm_id"}, {Key: "revenue_account", Scope: ScopeProduct}, {Key: "env", Scope: ScopeTenant}}}
	if p := info.ProductFields(); len(p) != 1 || p[0].Key != "revenue_account" {
		t.Fatalf("product fields = %+v", p)
	}
	if c := info.ConnectionFields(); len(c) != 2 || c[0].Key != "realm_id" || c[1].Key != "env" {
		t.Fatalf("connection fields = %+v", c)
	}
}
