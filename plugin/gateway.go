// Package plugin is the RackBill plugin SDK. Plugins (payment gateways,
// provisioners) run as separate processes speaking gRPC via
// hashicorp/go-plugin, so they can be written in any language and crash
// without taking the agent down.
package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"kubiq.io/rackbill/sdk/plugin/proto"
)

// Handshake guards against launching arbitrary binaries as plugins.
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "RACKBILL_PLUGIN",
	MagicCookieValue: "rackbill-gateway-v1",
}

// GatewayPluginName is the key gateways are served and dispensed under.
const GatewayPluginName = "gateway"

type GatewayInfo struct {
	Name            string   `json:"name"`
	DisplayName     string   `json:"display_name"`
	Version         string   `json:"version"`
	Maturity        string   `json:"maturity"` // Maturity* level (maturity.go)
	Currencies      []string `json:"currencies"`
	SupportsRefunds bool     `json:"supports_refunds"`
	// SupportsRecurringPayments promises hosted opt-in vaulting and subsequent
	// off-session charges. The provider holds the underlying payment details.
	SupportsRecurringPayments bool `json:"supports_recurring_payments"`
	// RecurringPaymentsConfigKey optionally names a tenant checkbox that must
	// be enabled before vaulting is offered (for provider merchant approval).
	RecurringPaymentsConfigKey string `json:"recurring_payments_config_key,omitempty"`
	// Positive only when BOTH Charge (Metadata["idempotency_key"]) and
	// Refund honor the key for at least this many seconds, across restarts.
	IdempotencyWindowSeconds int64 `json:"idempotency_window_seconds,omitempty"`
	SupportsRefundChecks     bool  `json:"supports_refund_checks,omitempty"`
	// Uses sdk/money currency units for Charge and Refund, including provider exceptions.
	CurrencyUnitsVersion int32 `json:"currency_units_version,omitempty"`
	// ConfigFields are tenant-level settings (API keys, checkout URLs)
	// rendered by the admin panel and delivered on every call via Config.
	// A gateway declaring none is configured instance-wide by env vars.
	ConfigFields []ConfigField `json:"config_fields"`
}

// RecurringPaymentsEnabled checks both adapter support and any merchant
// prerequisite. Checkbox values use the SDK's "1"/"0" convention; "true"
// remains accepted for API-written settings.
func (info GatewayInfo) RecurringPaymentsEnabled(config map[string]string) bool {
	if !info.SupportsRecurringPayments {
		return false
	}
	return info.RecurringPaymentsConfigKey == "" || config[info.RecurringPaymentsConfigKey] == "1" || config[info.RecurringPaymentsConfigKey] == "true"
}

type ChargeInput struct {
	// SavePaymentMethod is explicit customer consent to save a provider-held
	// method during hosted checkout. Never use it together with PaymentToken.
	SavePaymentMethod bool
	// PaymentToken is an opaque provider vault reference for an off-session
	// payment. It must never enter metadata, logs, receipts or browser JSON.
	PaymentToken string `json:"-"`
	TenantID     string
	AccountID    string
	InvoiceID    string
	AmountCents  int64
	Currency     string
	Metadata     map[string]string
	// Config carries the tenant's gateway settings (ConfigFields values).
	// Empty when the tenant has none — fall back to the environment.
	Config map[string]string
}

const (
	ChargeSucceeded = "succeeded"
	ChargeDeclined  = "declined"
	ChargePending   = "pending"
)

type ChargeResult struct {
	Status        string
	TransactionID string
	FailureReason string
	RedirectURL   string
	// RedirectParams, when non-empty, turns the checkout handoff into a form
	// POST: the panel submits them as hidden fields to RedirectURL instead of
	// navigating to it. For processors whose hosted page takes a POSTed token.
	RedirectParams map[string]string
}

type RefundInput struct {
	IdempotencyKey string
	TransactionID  string
	AmountCents    int64
	Currency       string
	Config         map[string]string // same semantics as ChargeInput.Config
}

type RefundResult struct {
	Status        string // RefundSucceeded, RefundFailed, RefundPending, RefundRequiresAction
	RefundID      string
	FailureReason string
}

const (
	RefundSucceeded      = "succeeded"
	RefundFailed         = "failed"
	RefundPending        = "pending"
	RefundRequiresAction = "requires_action"
)

type CheckRefundInput struct {
	RefundID string
	Config   map[string]string
}

// RefundChecker is an optional extension. A pending refund is never booked
// as successful until this read reports success.
type RefundChecker interface {
	CheckRefund(context.Context, CheckRefundInput) (RefundResult, error)
}

type CheckChargeInput struct {
	// RetrievePaymentMethod requests the vault reference only for a checkout
	// whose customer consent the host durably recorded before charging.
	RetrievePaymentMethod bool
	ExpectedAmountCents   int64
	ExpectedCurrency      string
	TransactionID         string
	Config                map[string]string // same semantics as ChargeInput.Config
}

// CheckChargeResult resolves a pending (hosted-checkout) charge. A non-empty
// TransactionID replaces the stored one — e.g. a checkout session id becomes
// the settled payment intent id, which refunds are issued against.
type CheckChargeResult struct {
	// PaymentToken is returned only after verified settlement and provider
	// vaulting. The host seals it before storage and never sends it to browsers.
	PaymentToken string `json:"-"`
	// AmountCents and Currency are provider-observed, not echoed request values.
	AmountCents   int64
	Currency      string
	Status        string // "succeeded", "declined", "pending"
	TransactionID string
	FailureReason string
}

// PaymentGateway is what a gateway plugin implements.
type PaymentGateway interface {
	Describe(ctx context.Context) (GatewayInfo, error)
	Charge(ctx context.Context, in ChargeInput) (ChargeResult, error)
	Refund(ctx context.Context, in RefundInput) (RefundResult, error)
	CheckCharge(ctx context.Context, in CheckChargeInput) (CheckChargeResult, error)
}

// GatewayPlugin adapts a PaymentGateway across the go-plugin gRPC boundary.
type GatewayPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl PaymentGateway
}

func (p *GatewayPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterPaymentGatewayServer(s, &gatewayGRPCServer{impl: p.Impl})
	return nil
}

func (p *GatewayPlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &gatewayGRPCClient{client: proto.NewPaymentGatewayClient(c)}, nil
}

// GatewayClient adapts a raw gRPC client into the PaymentGateway interface.
// The host uses it for satellite tunnels, where the same wire contract is
// spoken over a connection go-plugin did not dial.
func GatewayClient(c proto.PaymentGatewayClient) PaymentGateway {
	return &gatewayGRPCClient{client: c}
}

// GatewayServer adapts a PaymentGateway into the wire-facing gRPC server —
// the satellite relay resolves a local plugin per call and delegates here.
func GatewayServer(impl PaymentGateway) proto.PaymentGatewayServer {
	return &gatewayGRPCServer{impl: impl}
}

// ServeGateway is the entry point for gateway plugin binaries.
func ServeGateway(impl PaymentGateway) { ServeBundle(Bundle{Gateway: impl}) }

// --- gRPC adapters ---

type gatewayGRPCServer struct {
	proto.UnimplementedPaymentGatewayServer
	impl PaymentGateway
}

func (s *gatewayGRPCServer) Describe(ctx context.Context, _ *proto.DescribeRequest) (*proto.DescribeResponse, error) {
	info, err := s.impl.Describe(ctx)
	if err != nil {
		return nil, err
	}
	fields := configFieldsToProto(info.ConfigFields)
	return &proto.DescribeResponse{
		Name: info.Name, DisplayName: info.DisplayName, Version: info.Version, Maturity: info.Maturity,
		Currencies: info.Currencies, SupportsRefunds: info.SupportsRefunds,
		SupportsRecurringPayments:  info.SupportsRecurringPayments,
		RecurringPaymentsConfigKey: info.RecurringPaymentsConfigKey,
		IdempotencyWindowSeconds:   info.IdempotencyWindowSeconds, SupportsRefundChecks: info.SupportsRefundChecks, CurrencyUnitsVersion: info.CurrencyUnitsVersion,
		ConfigFields: fields,
	}, nil
}

func (s *gatewayGRPCServer) Charge(ctx context.Context, req *proto.ChargeRequest) (*proto.ChargeResponse, error) {
	res, err := s.impl.Charge(ctx, ChargeInput{
		TenantID: req.TenantId, AccountID: req.AccountId, InvoiceID: req.InvoiceId,
		AmountCents: req.AmountCents, Currency: req.Currency, Metadata: req.Metadata,
		Config:            req.Config,
		SavePaymentMethod: req.SavePaymentMethod, PaymentToken: req.PaymentToken,
	})
	if err != nil {
		return nil, err
	}
	return &proto.ChargeResponse{
		Status: res.Status, TransactionId: res.TransactionID,
		FailureReason: res.FailureReason, RedirectUrl: res.RedirectURL,
		RedirectParams: res.RedirectParams,
	}, nil
}

func (s *gatewayGRPCServer) Refund(ctx context.Context, req *proto.RefundRequest) (*proto.RefundResponse, error) {
	res, err := s.impl.Refund(ctx, RefundInput{
		IdempotencyKey: req.IdempotencyKey,
		TransactionID:  req.TransactionId, AmountCents: req.AmountCents, Currency: req.Currency,
		Config: req.Config,
	})
	if err != nil {
		return nil, err
	}
	return &proto.RefundResponse{Status: res.Status, RefundId: res.RefundID, FailureReason: res.FailureReason}, nil
}

func (s *gatewayGRPCServer) CheckRefund(ctx context.Context, req *proto.CheckRefundRequest) (*proto.RefundResponse, error) {
	checker, ok := s.impl.(RefundChecker)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "gateway does not support refund checks")
	}
	res, err := checker.CheckRefund(ctx, CheckRefundInput{RefundID: req.RefundId, Config: req.Config})
	if err != nil {
		return nil, err
	}
	return &proto.RefundResponse{Status: res.Status, RefundId: res.RefundID, FailureReason: res.FailureReason}, nil
}

func (s *gatewayGRPCServer) CheckCharge(ctx context.Context, req *proto.CheckChargeRequest) (*proto.CheckChargeResponse, error) {
	res, err := s.impl.CheckCharge(ctx, CheckChargeInput{TransactionID: req.TransactionId, Config: req.Config, ExpectedAmountCents: req.ExpectedAmountCents, ExpectedCurrency: req.ExpectedCurrency, RetrievePaymentMethod: req.RetrievePaymentMethod})
	if err != nil {
		return nil, err
	}
	return &proto.CheckChargeResponse{
		Status: res.Status, TransactionId: res.TransactionID, FailureReason: res.FailureReason,
		AmountCents: res.AmountCents, Currency: res.Currency,
		PaymentToken: res.PaymentToken,
	}, nil
}

type gatewayGRPCClient struct {
	client proto.PaymentGatewayClient
}

func (c *gatewayGRPCClient) Describe(ctx context.Context) (GatewayInfo, error) {
	resp, err := c.client.Describe(ctx, &proto.DescribeRequest{})
	if err != nil {
		return GatewayInfo{}, err
	}
	fields := configFieldsFromProto(resp.ConfigFields)
	return GatewayInfo{
		Name: resp.Name, DisplayName: resp.DisplayName, Version: resp.Version, Maturity: resp.Maturity,
		Currencies: resp.Currencies, SupportsRefunds: resp.SupportsRefunds,
		SupportsRecurringPayments:  resp.SupportsRecurringPayments,
		RecurringPaymentsConfigKey: resp.RecurringPaymentsConfigKey,
		IdempotencyWindowSeconds:   resp.IdempotencyWindowSeconds, SupportsRefundChecks: resp.SupportsRefundChecks, CurrencyUnitsVersion: resp.CurrencyUnitsVersion,
		ConfigFields: fields,
	}, nil
}

func (c *gatewayGRPCClient) Charge(ctx context.Context, in ChargeInput) (ChargeResult, error) {
	resp, err := c.client.Charge(ctx, &proto.ChargeRequest{
		TenantId: in.TenantID, AccountId: in.AccountID, InvoiceId: in.InvoiceID,
		AmountCents: in.AmountCents, Currency: in.Currency, Metadata: in.Metadata,
		Config:            in.Config,
		SavePaymentMethod: in.SavePaymentMethod, PaymentToken: in.PaymentToken,
	})
	if err != nil {
		return ChargeResult{}, err
	}
	return ChargeResult{
		Status: resp.Status, TransactionID: resp.TransactionId,
		FailureReason: resp.FailureReason, RedirectURL: resp.RedirectUrl,
		RedirectParams: resp.RedirectParams,
	}, nil
}

func (c *gatewayGRPCClient) Refund(ctx context.Context, in RefundInput) (RefundResult, error) {
	resp, err := c.client.Refund(ctx, &proto.RefundRequest{
		IdempotencyKey: in.IdempotencyKey,
		TransactionId:  in.TransactionID, AmountCents: in.AmountCents, Currency: in.Currency,
		Config: in.Config,
	})
	if err != nil {
		return RefundResult{}, err
	}
	return RefundResult{Status: resp.Status, RefundID: resp.RefundId, FailureReason: resp.FailureReason}, nil
}

func (c *gatewayGRPCClient) CheckRefund(ctx context.Context, in CheckRefundInput) (RefundResult, error) {
	resp, err := c.client.CheckRefund(ctx, &proto.CheckRefundRequest{RefundId: in.RefundID, Config: in.Config})
	if err != nil {
		return RefundResult{}, err
	}
	return RefundResult{Status: resp.Status, RefundID: resp.RefundId, FailureReason: resp.FailureReason}, nil
}

func (c *gatewayGRPCClient) CheckCharge(ctx context.Context, in CheckChargeInput) (CheckChargeResult, error) {
	resp, err := c.client.CheckCharge(ctx, &proto.CheckChargeRequest{TransactionId: in.TransactionID, Config: in.Config, ExpectedAmountCents: in.ExpectedAmountCents, ExpectedCurrency: in.ExpectedCurrency, RetrievePaymentMethod: in.RetrievePaymentMethod})
	if err != nil {
		return CheckChargeResult{}, err
	}
	return CheckChargeResult{
		Status: resp.Status, TransactionID: resp.TransactionId, FailureReason: resp.FailureReason,
		AmountCents: resp.AmountCents, Currency: resp.Currency,
		PaymentToken: resp.PaymentToken,
	}, nil
}
