package plugin

import (
	"context"
	"errors"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"rackbill.com/sdk/money"
	"rackbill.com/sdk/plugin/proto"
)

// AccountingPluginName is the key accounting providers are served and
// dispensed under.
const AccountingPluginName = "accounting"

// Sync statuses.
const (
	SyncSynced = "synced"
	SyncFailed = "failed"
)

// Connection test statuses.
const (
	TestOK     = "ok"
	TestFailed = "failed"
)

type AccountingInfo struct {
	Name            string `json:"name"`
	DisplayName     string `json:"display_name"`
	Version         string `json:"version"`
	Maturity        string `json:"maturity"` // Maturity* level (maturity.go)
	SupportsRefunds bool   `json:"supports_refunds"`
	// ConfigFields are the connection settings. A field with Scope
	// ScopeProduct (contract 15) is mapped per product instead, never
	// secret, and arrives as AccountingInvoiceLine.Config; DynamicOptions
	// makes it a dropdown filled by ListConfigOptions (ConfigOptionLister).
	ConfigFields []ConfigField `json:"config_fields"`
}

// ProductFields returns the fields mapped per product rather than entered on
// the connection (contract 15).
func (i AccountingInfo) ProductFields() []ConfigField {
	var out []ConfigField
	for _, f := range i.ConfigFields {
		if f.Scope == ScopeProduct {
			out = append(out, f)
		}
	}
	return out
}

// ConnectionFields returns the fields entered once on the company's
// accounting connection: every field without the product scope.
func (i AccountingInfo) ConnectionFields() []ConfigField {
	var out []ConfigField
	for _, f := range i.ConfigFields {
		if f.Scope != ScopeProduct {
			out = append(out, f)
		}
	}
	return out
}

// SyncResult answers every Sync* call. A non-empty ExternalRef replaces the
// stored ref for the record; ConfigUpdates carries rotated credentials the
// host persists into the tenant config before the next call.
type SyncResult struct {
	Status        string // SyncSynced, SyncFailed
	ExternalRef   string
	FailureReason string
	ConfigUpdates map[string]string
}

type TestConnectionInput struct {
	TenantID string
	Config   map[string]string
}

type TestConnectionResult struct {
	Status        string // TestOK, TestFailed
	Detail        string // connected company name on ok, reason on failed
	ConfigUpdates map[string]string
}

// AccountingCustomer is a billing account as the ledger sees it. ID is the
// stable RackBill account id — part of the natural key plugins adopt by.
type AccountingCustomer struct {
	ID       string
	Name     string
	Email    string
	Currency string
	Country  string
	Region   string
}

type SyncCustomerInput struct {
	TenantID    string
	Customer    AccountingCustomer
	ExternalRef string // from a prior sync; empty on first
	Config      map[string]string
}

type AccountingInvoiceLine struct {
	Description string
	Quantity    int64
	UnitCents   int64
	AmountCents int64
	Kind        string
	// Config is the line's product mapping for this provider: the values of
	// the plugin's product-scoped fields, snapshotted when the line was
	// billed. Empty means the connection default.
	Config map[string]string
}

// AccountingRefundLine is one part of a refund booked against one product
// mapping. The agent allocates; the plugin books.
type AccountingRefundLine struct {
	AmountCents int64 // gross, tax included
	Config      map[string]string
}

type AccountingInvoiceTax struct {
	Name        string
	RateBps     money.TaxRate
	AmountCents int64
}

type SyncInvoiceInput struct {
	TenantID      string
	InvoiceID     string
	Number        string // the natural key to adopt by
	Status        string
	Currency      string
	SubtotalCents int64
	TaxCents      int64
	TotalCents    int64
	IssuedAtUnix  int64
	DueAtUnix     int64 // 0 = none
	Lines         []AccountingInvoiceLine
	Taxes         []AccountingInvoiceTax
	CustomerRef   string // external ref of the already-synced customer
	ExternalRef   string
	Config        map[string]string
}

type SyncPaymentInput struct {
	TenantID      string
	PaymentID     string
	InvoiceRef    string // empty for credit top-ups
	CustomerRef   string
	AmountCents   int64
	Currency      string
	Gateway       string
	TransactionID string
	PaidAtUnix    int64
	ExternalRef   string
	Config        map[string]string
}

type SyncRefundInput struct {
	TenantID       string
	RefundID       string // stable per refund action — the natural key to adopt by
	PaymentRef     string // external ref of the synced payment
	CustomerRef    string
	AmountCents    int64
	Currency       string
	RefundedAtUnix int64
	ExternalRef    string
	Config         map[string]string
	// Lines splits the refund across product mappings, summing to
	// AmountCents. Empty means the connection default, as before contract 15.
	Lines []AccountingRefundLine
}

// AccountingOptions answers an accounting option enumeration: the values,
// plus any credentials the call rotated, which the host persists exactly as
// it does a sync's ConfigUpdates. Dropping them would lose a rotated OAuth
// refresh token. A provider that rejected the listing after the exchange
// rotated answers with FailureReason and a nil error, so the rotation still
// reaches the host; a returned error carries no updates.
type AccountingOptions struct {
	Options       []ConfigOption
	ConfigUpdates map[string]string
	FailureReason string
}

// AccountingOptionLister is the optional capability behind an accounting
// product field's DynamicOptions (contract 15). config is the company's
// stored connection config. A plugin implementing only ConfigOptionLister is
// served too, without rotations.
type AccountingOptionLister interface {
	ListAccountingOptions(ctx context.Context, tenantID, key string, config map[string]string) (AccountingOptions, error)
}

// AccountingProvider is what a bookkeeping plugin implements. A returned
// error means "couldn't reach the provider"; a declared failed status means
// the provider rejected the record — both count as failed attempts for retry
// purposes. Requests arrive with dependency refs already resolved by the
// agent; plugins never walk the object graph themselves.
type AccountingProvider interface {
	Describe(ctx context.Context) (AccountingInfo, error)
	TestConnection(ctx context.Context, in TestConnectionInput) (TestConnectionResult, error)
	SyncCustomer(ctx context.Context, in SyncCustomerInput) (SyncResult, error)
	SyncInvoice(ctx context.Context, in SyncInvoiceInput) (SyncResult, error)
	SyncPayment(ctx context.Context, in SyncPaymentInput) (SyncResult, error)
	SyncRefund(ctx context.Context, in SyncRefundInput) (SyncResult, error)
}

// AccountingPlugin adapts an AccountingProvider across the go-plugin gRPC
// boundary.
type AccountingPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl AccountingProvider
}

func (p *AccountingPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterAccountingServer(s, &accountingGRPCServer{impl: p.Impl})
	return nil
}

func (p *AccountingPlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &accountingGRPCClient{client: proto.NewAccountingClient(c)}, nil
}

// ServeAccounting is the entry point for accounting plugin binaries.
func ServeAccounting(impl AccountingProvider) { ServeBundle(Bundle{Accounting: impl}) }

// --- gRPC adapters ---

type accountingGRPCServer struct {
	proto.UnimplementedAccountingServer
	impl AccountingProvider
}

func syncResponse(res SyncResult) *proto.AccountingSyncResponse {
	return &proto.AccountingSyncResponse{
		Status: res.Status, ExternalRef: res.ExternalRef,
		FailureReason: res.FailureReason, ConfigUpdates: res.ConfigUpdates,
	}
}

func (s *accountingGRPCServer) DescribeAccounting(ctx context.Context, _ *proto.DescribeAccountingRequest) (*proto.DescribeAccountingResponse, error) {
	info, err := s.impl.Describe(ctx)
	if err != nil {
		return nil, err
	}
	fields := configFieldsToProto(info.ConfigFields)
	return &proto.DescribeAccountingResponse{
		Name: info.Name, DisplayName: info.DisplayName, Version: info.Version, Maturity: info.Maturity,
		SupportsRefunds: info.SupportsRefunds, ConfigFields: fields,
		SupportsFractionalTaxRates: true,
	}, nil
}

func (s *accountingGRPCServer) TestAccountingConnection(ctx context.Context, req *proto.TestAccountingConnectionRequest) (*proto.TestAccountingConnectionResponse, error) {
	res, err := s.impl.TestConnection(ctx, TestConnectionInput{TenantID: req.TenantId, Config: req.Config})
	if err != nil {
		return nil, err
	}
	return &proto.TestAccountingConnectionResponse{
		Status: res.Status, Detail: res.Detail, ConfigUpdates: res.ConfigUpdates,
	}, nil
}

func (s *accountingGRPCServer) SyncCustomer(ctx context.Context, req *proto.SyncCustomerRequest) (*proto.AccountingSyncResponse, error) {
	in := SyncCustomerInput{TenantID: req.TenantId, ExternalRef: req.ExternalRef, Config: req.Config}
	if c := req.Customer; c != nil {
		in.Customer = AccountingCustomer{
			ID: c.Id, Name: c.Name, Email: c.Email,
			Currency: c.Currency, Country: c.Country, Region: c.Region,
		}
	}
	res, err := s.impl.SyncCustomer(ctx, in)
	if err != nil {
		return nil, err
	}
	return syncResponse(res), nil
}

func (s *accountingGRPCServer) SyncInvoice(ctx context.Context, req *proto.SyncInvoiceRequest) (*proto.AccountingSyncResponse, error) {
	in := SyncInvoiceInput{
		TenantID: req.TenantId, InvoiceID: req.InvoiceId, Number: req.Number,
		Status: req.Status, Currency: req.Currency,
		SubtotalCents: req.SubtotalCents, TaxCents: req.TaxCents, TotalCents: req.TotalCents,
		IssuedAtUnix: req.IssuedAtUnix, DueAtUnix: req.DueAtUnix,
		CustomerRef: req.CustomerRef, ExternalRef: req.ExternalRef, Config: req.Config,
	}
	for _, l := range req.Lines {
		in.Lines = append(in.Lines, AccountingInvoiceLine{
			Description: l.Description, Quantity: l.Quantity,
			UnitCents: l.UnitCents, AmountCents: l.AmountCents, Kind: l.Kind,
			Config: l.Config,
		})
	}
	for _, t := range req.Taxes {
		rate := money.TaxRate(t.RateBps)
		if t.RateUnits != 0 {
			rate = money.TaxRate(float64(t.RateUnits) / float64(money.TaxRateScale))
		}
		if _, err := rate.Units(); err != nil {
			return nil, err
		}
		in.Taxes = append(in.Taxes, AccountingInvoiceTax{
			Name: t.Name, RateBps: rate, AmountCents: t.AmountCents,
		})
	}
	res, err := s.impl.SyncInvoice(ctx, in)
	if err != nil {
		return nil, err
	}
	return syncResponse(res), nil
}

func (s *accountingGRPCServer) SyncPayment(ctx context.Context, req *proto.SyncPaymentRequest) (*proto.AccountingSyncResponse, error) {
	res, err := s.impl.SyncPayment(ctx, SyncPaymentInput{
		TenantID: req.TenantId, PaymentID: req.PaymentId,
		InvoiceRef: req.InvoiceRef, CustomerRef: req.CustomerRef,
		AmountCents: req.AmountCents, Currency: req.Currency,
		Gateway: req.Gateway, TransactionID: req.TransactionId,
		PaidAtUnix: req.PaidAtUnix, ExternalRef: req.ExternalRef, Config: req.Config,
	})
	if err != nil {
		return nil, err
	}
	return syncResponse(res), nil
}

func (s *accountingGRPCServer) SyncRefund(ctx context.Context, req *proto.SyncRefundRequest) (*proto.AccountingSyncResponse, error) {
	in := SyncRefundInput{
		TenantID: req.TenantId, RefundID: req.RefundId,
		PaymentRef: req.PaymentRef, CustomerRef: req.CustomerRef,
		AmountCents: req.AmountCents, Currency: req.Currency,
		RefundedAtUnix: req.RefundedAtUnix, ExternalRef: req.ExternalRef, Config: req.Config,
	}
	for _, l := range req.Lines {
		in.Lines = append(in.Lines, AccountingRefundLine{AmountCents: l.AmountCents, Config: l.Config})
	}
	res, err := s.impl.SyncRefund(ctx, in)
	if err != nil {
		return nil, err
	}
	return syncResponse(res), nil
}

func (s *accountingGRPCServer) ListConfigOptions(ctx context.Context, req *proto.ListConfigOptionsRequest) (*proto.ListConfigOptionsResponse, error) {
	if m, ok := s.impl.(AccountingOptionLister); ok {
		res, err := m.ListAccountingOptions(ctx, req.TenantId, req.Key, req.Config)
		if err != nil {
			return nil, err
		}
		return &proto.ListConfigOptionsResponse{Options: configOptionsToProto(res.Options), ConfigUpdates: res.ConfigUpdates, FailureReason: res.FailureReason}, nil
	}
	m, ok := s.impl.(ConfigOptionLister)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement config-option enumeration")
	}
	opts, err := m.ListConfigOptions(ctx, req.TenantId, req.Key, req.Config)
	if err != nil {
		return nil, err
	}
	return &proto.ListConfigOptionsResponse{Options: configOptionsToProto(opts)}, nil
}

type accountingGRPCClient struct {
	client proto.AccountingClient
}

func syncResult(resp *proto.AccountingSyncResponse) SyncResult {
	return SyncResult{
		Status: resp.Status, ExternalRef: resp.ExternalRef,
		FailureReason: resp.FailureReason, ConfigUpdates: resp.ConfigUpdates,
	}
}

func (c *accountingGRPCClient) Describe(ctx context.Context) (AccountingInfo, error) {
	resp, err := c.client.DescribeAccounting(ctx, &proto.DescribeAccountingRequest{})
	if err != nil {
		return AccountingInfo{}, err
	}
	fields := configFieldsFromProto(resp.ConfigFields)
	return AccountingInfo{
		Name: resp.Name, DisplayName: resp.DisplayName, Version: resp.Version, Maturity: resp.Maturity,
		SupportsRefunds: resp.SupportsRefunds, ConfigFields: fields,
	}, nil
}

func (c *accountingGRPCClient) TestConnection(ctx context.Context, in TestConnectionInput) (TestConnectionResult, error) {
	resp, err := c.client.TestAccountingConnection(ctx, &proto.TestAccountingConnectionRequest{
		TenantId: in.TenantID, Config: in.Config,
	})
	if err != nil {
		return TestConnectionResult{}, err
	}
	return TestConnectionResult{Status: resp.Status, Detail: resp.Detail, ConfigUpdates: resp.ConfigUpdates}, nil
}

func (c *accountingGRPCClient) SyncCustomer(ctx context.Context, in SyncCustomerInput) (SyncResult, error) {
	resp, err := c.client.SyncCustomer(ctx, &proto.SyncCustomerRequest{
		TenantId: in.TenantID,
		Customer: &proto.AccountingCustomer{
			Id: in.Customer.ID, Name: in.Customer.Name, Email: in.Customer.Email,
			Currency: in.Customer.Currency, Country: in.Customer.Country, Region: in.Customer.Region,
		},
		ExternalRef: in.ExternalRef, Config: in.Config,
	})
	if err != nil {
		return SyncResult{}, err
	}
	return syncResult(resp), nil
}

func (c *accountingGRPCClient) SyncInvoice(ctx context.Context, in SyncInvoiceInput) (SyncResult, error) {
	req := &proto.SyncInvoiceRequest{
		TenantId: in.TenantID, InvoiceId: in.InvoiceID, Number: in.Number,
		Status: in.Status, Currency: in.Currency,
		SubtotalCents: in.SubtotalCents, TaxCents: in.TaxCents, TotalCents: in.TotalCents,
		IssuedAtUnix: in.IssuedAtUnix, DueAtUnix: in.DueAtUnix,
		CustomerRef: in.CustomerRef, ExternalRef: in.ExternalRef, Config: in.Config,
	}
	for _, l := range in.Lines {
		req.Lines = append(req.Lines, &proto.AccountingInvoiceLine{
			Description: l.Description, Quantity: l.Quantity,
			UnitCents: l.UnitCents, AmountCents: l.AmountCents, Kind: l.Kind,
			Config: l.Config,
		})
	}
	for _, t := range in.Taxes {
		units, err := t.RateBps.Units()
		if err != nil {
			return SyncResult{}, err
		}
		legacyBps := int32(0)
		if units%money.TaxRateScale == 0 {
			legacyBps = int32(units / money.TaxRateScale)
		} else {
			info, err := c.client.DescribeAccounting(ctx, &proto.DescribeAccountingRequest{})
			if err != nil {
				return SyncResult{}, err
			}
			if !info.SupportsFractionalTaxRates {
				return SyncResult{}, errors.New("accounting plugin must be upgraded to export fractional tax rates")
			}
		}
		req.Taxes = append(req.Taxes, &proto.AccountingInvoiceTax{
			Name: t.Name, RateBps: legacyBps, RateUnits: units, AmountCents: t.AmountCents,
		})
	}
	resp, err := c.client.SyncInvoice(ctx, req)
	if err != nil {
		return SyncResult{}, err
	}
	return syncResult(resp), nil
}

func (c *accountingGRPCClient) SyncPayment(ctx context.Context, in SyncPaymentInput) (SyncResult, error) {
	resp, err := c.client.SyncPayment(ctx, &proto.SyncPaymentRequest{
		TenantId: in.TenantID, PaymentId: in.PaymentID,
		InvoiceRef: in.InvoiceRef, CustomerRef: in.CustomerRef,
		AmountCents: in.AmountCents, Currency: in.Currency,
		Gateway: in.Gateway, TransactionId: in.TransactionID,
		PaidAtUnix: in.PaidAtUnix, ExternalRef: in.ExternalRef, Config: in.Config,
	})
	if err != nil {
		return SyncResult{}, err
	}
	return syncResult(resp), nil
}

func (c *accountingGRPCClient) SyncRefund(ctx context.Context, in SyncRefundInput) (SyncResult, error) {
	req := &proto.SyncRefundRequest{
		TenantId: in.TenantID, RefundId: in.RefundID,
		PaymentRef: in.PaymentRef, CustomerRef: in.CustomerRef,
		AmountCents: in.AmountCents, Currency: in.Currency,
		RefundedAtUnix: in.RefundedAtUnix, ExternalRef: in.ExternalRef, Config: in.Config,
	}
	for _, l := range in.Lines {
		req.Lines = append(req.Lines, &proto.AccountingRefundLine{AmountCents: l.AmountCents, Config: l.Config})
	}
	resp, err := c.client.SyncRefund(ctx, req)
	if err != nil {
		return SyncResult{}, err
	}
	return syncResult(resp), nil
}

// The client side always exposes AccountingOptionLister, never the bare
// ConfigOptionLister, so a host cannot drop rotated credentials by accident;
// the host only routes the call for fields declared with DynamicOptions.

func (c *accountingGRPCClient) ListAccountingOptions(ctx context.Context, tenantID, key string, config map[string]string) (AccountingOptions, error) {
	resp, err := c.client.ListConfigOptions(ctx, &proto.ListConfigOptionsRequest{
		TenantId: tenantID, Key: key, Config: config,
	})
	if err != nil {
		return AccountingOptions{}, err
	}
	return AccountingOptions{Options: configOptionsFromProto(resp.Options), ConfigUpdates: resp.ConfigUpdates, FailureReason: resp.FailureReason}, nil
}
