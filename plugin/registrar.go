package plugin

import (
	"context"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"kubiq.io/rackbill/sdk/plugin/proto"
)

// RegistrarPluginName is the key registrars are served and dispensed under.
const RegistrarPluginName = "registrar"

// Domain operation statuses (mutating calls).
const (
	DomainOpSucceeded = "succeeded"
	DomainOpPending   = "pending"
	DomainOpFailed    = "failed"
)

// Domain statuses reported by GetDomainStatus.
const (
	DomainActive  = "active"
	DomainPending = "pending"
	DomainFailed  = "failed"
	DomainExpired = "expired"
)

type RegistrarInfo struct {
	// MaxRegistrationYears is the initial registration limit; zero means one.
	MaxRegistrationYears int      `json:"max_registration_years"`
	Name                 string   `json:"name"`
	DisplayName          string   `json:"display_name"`
	Version              string   `json:"version"`
	Maturity             string   `json:"maturity"` // Maturity* level (maturity.go)
	TLDs                 []string `json:"tlds"`
	SupportsTransfers    bool     `json:"supports_transfers"`
	SupportsDNSSEC       bool     `json:"supports_dnssec"`
	SupportsWhoisPrivacy bool     `json:"supports_whois_privacy"`
	// SupportsTLDPrices: the impl also satisfies TLDPricer.
	SupportsTLDPrices bool `json:"supports_tld_prices"`
	// SupportsDNSHosting: the impl also satisfies ZoneHoster.
	SupportsDNSHosting bool          `json:"supports_dns_hosting"`
	ConfigFields       []ConfigField `json:"config_fields"`
}

// TLDPrice is one TLD's cost to the operator, standard tier, one year per
// operation. A zero price means the provider does not offer (or did not
// disclose) that operation for the TLD — never "free".
type TLDPrice struct {
	TLD           string `json:"tld"`      // without dot; multi-label allowed ("co.uk")
	Currency      string `json:"currency"` // ISO 4217 code the provider bills in
	RegisterCents int64  `json:"register_cents"`
	RenewCents    int64  `json:"renew_cents"`
	TransferCents int64  `json:"transfer_cents"`
}

// TLDPricer is the optional cost-sheet capability of a registrar (§10.1).
// Implement it alongside Registrar and set SupportsTLDPrices in Describe.
// config is the tenant's registrar *account* settings, not a product's —
// the pull happens before products for the TLDs exist. The whole sheet must
// come back inside a ~90s budget; a provider that can only price one TLD
// per round trip must not declare the capability.
type TLDPricer interface {
	ListTLDPrices(ctx context.Context, tenantID string, config map[string]string) ([]TLDPrice, error)
}

// Zone record types a hosted zone carries (A, AAAA, CNAME and TXT are the
// DNS kind's Record* constants).
const (
	RecordMX  = "MX"
	RecordSRV = "SRV"
	RecordCAA = "CAA"
)

// ZoneRecord is one record in a hosted zone. Name is relative to the zone
// with no trailing dot, "" for the apex. Value carries no priority: an MX
// value is the host, an SRV value is "weight port target", a CAA value is
// "flags tag value". Priority is set for MX and SRV only.
type ZoneRecord struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Value      string `json:"value"`
	TTLSeconds int    `json:"ttl_seconds"`
	Priority   int    `json:"priority"`
}

// ZoneOpResult is the outcome of PutZone or DeleteZone. Zone operations
// never pend: Status is DomainOpSucceeded or DomainOpFailed.
type ZoneOpResult struct {
	Status        string
	FailureReason string
	// Nameservers (PutZone) are the hosts serving the zone, which the
	// domain's NS must name for the zone to answer.
	Nameservers []string
}

// ZoneHoster is the optional DNS hosting capability of a registrar.
// Implement it alongside Registrar and set SupportsDNSHosting in Describe.
// The agent owns the record set and sends all of it; the provider keeps its
// own SOA and apex NS, which never travel in either direction. ref.Domain
// is the zone and ref.Config the product's registrar config.
type ZoneHoster interface {
	// ListZoneRecords reads the zone the provider serves now; found is false
	// when it has none. Used once, to import a zone when hosting starts.
	ListZoneRecords(ctx context.Context, ref DomainRef) (records []ZoneRecord, found bool, err error)
	// PutZone creates the zone when absent and makes its records exactly
	// records. Retried at-least-once, so it must be idempotent.
	PutZone(ctx context.Context, ref DomainRef, records []ZoneRecord) (ZoneOpResult, error)
	// DeleteZone removes the zone; an absent zone is success.
	DeleteZone(ctx context.Context, ref DomainRef) (ZoneOpResult, error)
}

// DomainRef identifies a domain on registrar calls.
type DomainRef struct {
	TenantID  string
	ServiceID string
	Domain    string
	Config    map[string]string
}

type RegisterInput struct {
	DomainRef
	Years       int
	Nameservers []string
	Contact     map[string]string
}

type RenewInput struct {
	DomainRef
	Years          int
	CurrentExpires time.Time
}

type TransferInput struct {
	DomainRef
	AuthCode string
	Contact  map[string]string
}

// DomainOpResult is the outcome of a mutating call. Pending results must
// carry an OrderRef the agent polls GetDomainStatus with.
type DomainOpResult struct {
	Status        string
	OrderRef      string
	Expires       time.Time // zero = unchanged/unknown
	FailureReason string
}

type DomainStatus struct {
	Status        string
	Expires       time.Time
	Nameservers   []string
	FailureReason string
}

// Registrar is what a domain registrar plugin implements. The usual split:
// a returned error is a transport problem, a declared failed status is the
// registry's answer. Operations are retried at-least-once — registering an
// already-registered-to-this-order domain must return succeeded.
type Registrar interface {
	Describe(ctx context.Context) (RegistrarInfo, error)
	CheckAvailability(ctx context.Context, tenantID, domain string, config map[string]string) (available bool, reason string, err error)
	Register(ctx context.Context, in RegisterInput) (DomainOpResult, error)
	Renew(ctx context.Context, in RenewInput) (DomainOpResult, error)
	Transfer(ctx context.Context, in TransferInput) (DomainOpResult, error)
	SetNameservers(ctx context.Context, ref DomainRef, nameservers []string) (DomainOpResult, error)
	Status(ctx context.Context, ref DomainRef, orderRef string) (DomainStatus, error)
}

// RegistrarPlugin adapts a Registrar across the go-plugin gRPC boundary.
type RegistrarPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl Registrar
}

func (p *RegistrarPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterRegistrarServer(s, &registrarGRPCServer{impl: p.Impl})
	return nil
}

func (p *RegistrarPlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &registrarGRPCClient{client: proto.NewRegistrarClient(c)}, nil
}

// ServeRegistrar is the entry point for registrar plugin binaries.
func ServeRegistrar(impl Registrar) { ServeBundle(Bundle{Registrar: impl}) }

// --- gRPC adapters ---

type registrarGRPCServer struct {
	proto.UnimplementedRegistrarServer
	impl Registrar
}

func (s *registrarGRPCServer) DescribeRegistrar(ctx context.Context, _ *proto.DescribeRegistrarRequest) (*proto.DescribeRegistrarResponse, error) {
	info, err := s.impl.Describe(ctx)
	if err != nil {
		return nil, err
	}
	fields := configFieldsToProto(info.ConfigFields)
	return &proto.DescribeRegistrarResponse{
		Name: info.Name, DisplayName: info.DisplayName, Version: info.Version, Maturity: info.Maturity,
		Tlds: info.TLDs, SupportsTransfers: info.SupportsTransfers,
		SupportsDnssec: info.SupportsDNSSEC, SupportsWhoisPrivacy: info.SupportsWhoisPrivacy,
		MaxRegistrationYears: int32(info.MaxRegistrationYears), SupportsTldPrices: info.SupportsTLDPrices, SupportsDnsHosting: info.SupportsDNSHosting,
		ConfigFields: fields,
	}, nil
}

func zoneRecordsToProto(in []ZoneRecord) []*proto.ZoneRecord {
	out := make([]*proto.ZoneRecord, len(in))
	for i, r := range in {
		out[i] = &proto.ZoneRecord{Name: r.Name, Type: r.Type, Value: r.Value, TtlSeconds: int32(r.TTLSeconds), Priority: int32(r.Priority)}
	}
	return out
}

func zoneRecordsFromProto(in []*proto.ZoneRecord) []ZoneRecord {
	out := make([]ZoneRecord, len(in))
	for i, r := range in {
		out[i] = ZoneRecord{Name: r.Name, Type: r.Type, Value: r.Value, TTLSeconds: int(r.TtlSeconds), Priority: int(r.Priority)}
	}
	return out
}

func (s *registrarGRPCServer) zoneHoster() (ZoneHoster, error) {
	z, ok := s.impl.(ZoneHoster)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the dns-hosting capability")
	}
	return z, nil
}

func (s *registrarGRPCServer) ListZoneRecords(ctx context.Context, req *proto.ZoneRequest) (*proto.ListZoneRecordsResponse, error) {
	z, err := s.zoneHoster()
	if err != nil {
		return nil, err
	}
	records, found, err := z.ListZoneRecords(ctx, DomainRef{TenantID: req.TenantId, ServiceID: req.ServiceId, Domain: req.Domain, Config: req.Config})
	if err != nil {
		return nil, err
	}
	return &proto.ListZoneRecordsResponse{Found: found, Records: zoneRecordsToProto(records)}, nil
}

func (s *registrarGRPCServer) PutZone(ctx context.Context, req *proto.PutZoneRequest) (*proto.ZoneOpResponse, error) {
	z, err := s.zoneHoster()
	if err != nil {
		return nil, err
	}
	res, err := z.PutZone(ctx, DomainRef{TenantID: req.TenantId, ServiceID: req.ServiceId, Domain: req.Domain, Config: req.Config}, zoneRecordsFromProto(req.Records))
	if err != nil {
		return nil, err
	}
	return &proto.ZoneOpResponse{Status: res.Status, FailureReason: res.FailureReason, Nameservers: res.Nameservers}, nil
}

func (s *registrarGRPCServer) DeleteZone(ctx context.Context, req *proto.ZoneRequest) (*proto.ZoneOpResponse, error) {
	z, err := s.zoneHoster()
	if err != nil {
		return nil, err
	}
	res, err := z.DeleteZone(ctx, DomainRef{TenantID: req.TenantId, ServiceID: req.ServiceId, Domain: req.Domain, Config: req.Config})
	if err != nil {
		return nil, err
	}
	return &proto.ZoneOpResponse{Status: res.Status, FailureReason: res.FailureReason}, nil
}

func (s *registrarGRPCServer) ListTLDPrices(ctx context.Context, req *proto.ListTLDPricesRequest) (*proto.ListTLDPricesResponse, error) {
	p, ok := s.impl.(TLDPricer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the tld-prices capability")
	}
	prices, err := p.ListTLDPrices(ctx, req.TenantId, req.Config)
	if err != nil {
		return nil, err
	}
	out := make([]*proto.TLDPrice, len(prices))
	for i, tp := range prices {
		out[i] = &proto.TLDPrice{
			Tld: tp.TLD, Currency: tp.Currency,
			RegisterCents: tp.RegisterCents, RenewCents: tp.RenewCents, TransferCents: tp.TransferCents,
		}
	}
	return &proto.ListTLDPricesResponse{Prices: out}, nil
}

func (s *registrarGRPCServer) CheckAvailability(ctx context.Context, req *proto.CheckAvailabilityRequest) (*proto.CheckAvailabilityResponse, error) {
	available, reason, err := s.impl.CheckAvailability(ctx, req.TenantId, req.Domain, req.Config)
	if err != nil {
		return nil, err
	}
	return &proto.CheckAvailabilityResponse{Available: available, Reason: reason}, nil
}

func opResponse(res DomainOpResult) *proto.DomainOpResponse {
	var expires int64
	if !res.Expires.IsZero() {
		expires = res.Expires.Unix()
	}
	return &proto.DomainOpResponse{
		Status: res.Status, OrderRef: res.OrderRef,
		ExpiresUnix: expires, FailureReason: res.FailureReason,
	}
}

func (s *registrarGRPCServer) RegisterDomain(ctx context.Context, req *proto.RegisterDomainRequest) (*proto.DomainOpResponse, error) {
	res, err := s.impl.Register(ctx, RegisterInput{
		DomainRef: DomainRef{TenantID: req.TenantId, ServiceID: req.ServiceId, Domain: req.Domain, Config: req.Config},
		Years:     int(req.Years), Nameservers: req.Nameservers, Contact: req.Contact,
	})
	if err != nil {
		return nil, err
	}
	return opResponse(res), nil
}

func (s *registrarGRPCServer) RenewDomain(ctx context.Context, req *proto.RenewDomainRequest) (*proto.DomainOpResponse, error) {
	var cur time.Time
	if req.CurrentExpiresUnix > 0 {
		cur = time.Unix(req.CurrentExpiresUnix, 0).UTC()
	}
	res, err := s.impl.Renew(ctx, RenewInput{
		DomainRef: DomainRef{TenantID: req.TenantId, ServiceID: req.ServiceId, Domain: req.Domain, Config: req.Config},
		Years:     int(req.Years), CurrentExpires: cur,
	})
	if err != nil {
		return nil, err
	}
	return opResponse(res), nil
}

func (s *registrarGRPCServer) TransferDomain(ctx context.Context, req *proto.TransferDomainRequest) (*proto.DomainOpResponse, error) {
	res, err := s.impl.Transfer(ctx, TransferInput{
		DomainRef: DomainRef{TenantID: req.TenantId, ServiceID: req.ServiceId, Domain: req.Domain, Config: req.Config},
		AuthCode:  req.AuthCode, Contact: req.Contact,
	})
	if err != nil {
		return nil, err
	}
	return opResponse(res), nil
}

func (s *registrarGRPCServer) SetNameservers(ctx context.Context, req *proto.SetNameserversRequest) (*proto.DomainOpResponse, error) {
	res, err := s.impl.SetNameservers(ctx,
		DomainRef{TenantID: req.TenantId, ServiceID: req.ServiceId, Domain: req.Domain, Config: req.Config},
		req.Nameservers)
	if err != nil {
		return nil, err
	}
	return opResponse(res), nil
}

func (s *registrarGRPCServer) GetDomainStatus(ctx context.Context, req *proto.GetDomainStatusRequest) (*proto.DomainStatusResponse, error) {
	st, err := s.impl.Status(ctx,
		DomainRef{TenantID: req.TenantId, ServiceID: req.ServiceId, Domain: req.Domain, Config: req.Config},
		req.OrderRef)
	if err != nil {
		return nil, err
	}
	var expires int64
	if !st.Expires.IsZero() {
		expires = st.Expires.Unix()
	}
	return &proto.DomainStatusResponse{
		Status: st.Status, ExpiresUnix: expires,
		Nameservers: st.Nameservers, FailureReason: st.FailureReason,
	}, nil
}

type registrarGRPCClient struct {
	client proto.RegistrarClient
}

func (c *registrarGRPCClient) Describe(ctx context.Context) (RegistrarInfo, error) {
	resp, err := c.client.DescribeRegistrar(ctx, &proto.DescribeRegistrarRequest{})
	if err != nil {
		return RegistrarInfo{}, err
	}
	fields := configFieldsFromProto(resp.ConfigFields)
	return RegistrarInfo{
		Name: resp.Name, DisplayName: resp.DisplayName, Version: resp.Version, Maturity: resp.Maturity,
		TLDs: resp.Tlds, SupportsTransfers: resp.SupportsTransfers,
		SupportsDNSSEC: resp.SupportsDnssec, SupportsWhoisPrivacy: resp.SupportsWhoisPrivacy,
		MaxRegistrationYears: int(resp.MaxRegistrationYears), SupportsTLDPrices: resp.SupportsTldPrices, SupportsDNSHosting: resp.SupportsDnsHosting,
		ConfigFields: fields,
	}, nil
}

func (c *registrarGRPCClient) ListZoneRecords(ctx context.Context, ref DomainRef) ([]ZoneRecord, bool, error) {
	resp, err := c.client.ListZoneRecords(ctx, &proto.ZoneRequest{
		TenantId: ref.TenantID, ServiceId: ref.ServiceID, Domain: ref.Domain, Config: ref.Config,
	})
	if err != nil {
		return nil, false, err
	}
	return zoneRecordsFromProto(resp.Records), resp.Found, nil
}

func (c *registrarGRPCClient) PutZone(ctx context.Context, ref DomainRef, records []ZoneRecord) (ZoneOpResult, error) {
	resp, err := c.client.PutZone(ctx, &proto.PutZoneRequest{
		TenantId: ref.TenantID, ServiceId: ref.ServiceID, Domain: ref.Domain,
		Records: zoneRecordsToProto(records), Config: ref.Config,
	})
	if err != nil {
		return ZoneOpResult{}, err
	}
	return ZoneOpResult{Status: resp.Status, FailureReason: resp.FailureReason, Nameservers: resp.Nameservers}, nil
}

func (c *registrarGRPCClient) DeleteZone(ctx context.Context, ref DomainRef) (ZoneOpResult, error) {
	resp, err := c.client.DeleteZone(ctx, &proto.ZoneRequest{
		TenantId: ref.TenantID, ServiceId: ref.ServiceID, Domain: ref.Domain, Config: ref.Config,
	})
	if err != nil {
		return ZoneOpResult{}, err
	}
	return ZoneOpResult{Status: resp.Status, FailureReason: resp.FailureReason}, nil
}

func (c *registrarGRPCClient) ListTLDPrices(ctx context.Context, tenantID string, config map[string]string) ([]TLDPrice, error) {
	resp, err := c.client.ListTLDPrices(ctx, &proto.ListTLDPricesRequest{TenantId: tenantID, Config: config})
	if err != nil {
		return nil, err
	}
	out := make([]TLDPrice, len(resp.Prices))
	for i, tp := range resp.Prices {
		out[i] = TLDPrice{
			TLD: tp.Tld, Currency: tp.Currency,
			RegisterCents: tp.RegisterCents, RenewCents: tp.RenewCents, TransferCents: tp.TransferCents,
		}
	}
	return out, nil
}

func (c *registrarGRPCClient) CheckAvailability(ctx context.Context, tenantID, domain string, config map[string]string) (bool, string, error) {
	resp, err := c.client.CheckAvailability(ctx, &proto.CheckAvailabilityRequest{
		TenantId: tenantID, Domain: domain, Config: config,
	})
	if err != nil {
		return false, "", err
	}
	return resp.Available, resp.Reason, nil
}

func opResult(resp *proto.DomainOpResponse) DomainOpResult {
	res := DomainOpResult{
		Status: resp.Status, OrderRef: resp.OrderRef, FailureReason: resp.FailureReason,
	}
	if resp.ExpiresUnix > 0 {
		res.Expires = time.Unix(resp.ExpiresUnix, 0).UTC()
	}
	return res
}

func (c *registrarGRPCClient) Register(ctx context.Context, in RegisterInput) (DomainOpResult, error) {
	resp, err := c.client.RegisterDomain(ctx, &proto.RegisterDomainRequest{
		TenantId: in.TenantID, ServiceId: in.ServiceID, Domain: in.Domain,
		Years: int32(in.Years), Nameservers: in.Nameservers, Contact: in.Contact, Config: in.Config,
	})
	if err != nil {
		return DomainOpResult{}, err
	}
	return opResult(resp), nil
}

func (c *registrarGRPCClient) Renew(ctx context.Context, in RenewInput) (DomainOpResult, error) {
	var cur int64
	if !in.CurrentExpires.IsZero() {
		cur = in.CurrentExpires.Unix()
	}
	resp, err := c.client.RenewDomain(ctx, &proto.RenewDomainRequest{
		TenantId: in.TenantID, ServiceId: in.ServiceID, Domain: in.Domain,
		Years: int32(in.Years), CurrentExpiresUnix: cur, Config: in.Config,
	})
	if err != nil {
		return DomainOpResult{}, err
	}
	return opResult(resp), nil
}

func (c *registrarGRPCClient) Transfer(ctx context.Context, in TransferInput) (DomainOpResult, error) {
	resp, err := c.client.TransferDomain(ctx, &proto.TransferDomainRequest{
		TenantId: in.TenantID, ServiceId: in.ServiceID, Domain: in.Domain,
		AuthCode: in.AuthCode, Contact: in.Contact, Config: in.Config,
	})
	if err != nil {
		return DomainOpResult{}, err
	}
	return opResult(resp), nil
}

func (c *registrarGRPCClient) SetNameservers(ctx context.Context, ref DomainRef, nameservers []string) (DomainOpResult, error) {
	resp, err := c.client.SetNameservers(ctx, &proto.SetNameserversRequest{
		TenantId: ref.TenantID, ServiceId: ref.ServiceID, Domain: ref.Domain,
		Nameservers: nameservers, Config: ref.Config,
	})
	if err != nil {
		return DomainOpResult{}, err
	}
	return opResult(resp), nil
}

func (c *registrarGRPCClient) Status(ctx context.Context, ref DomainRef, orderRef string) (DomainStatus, error) {
	resp, err := c.client.GetDomainStatus(ctx, &proto.GetDomainStatusRequest{
		TenantId: ref.TenantID, ServiceId: ref.ServiceID, Domain: ref.Domain,
		OrderRef: orderRef, Config: ref.Config,
	})
	if err != nil {
		return DomainStatus{}, err
	}
	st := DomainStatus{
		Status: resp.Status, Nameservers: resp.Nameservers, FailureReason: resp.FailureReason,
	}
	if resp.ExpiresUnix > 0 {
		st.Expires = time.Unix(resp.ExpiresUnix, 0).UTC()
	}
	return st, nil
}
