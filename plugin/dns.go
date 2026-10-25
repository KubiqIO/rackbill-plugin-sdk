package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"kubiq.io/rackbill/sdk/plugin/proto"
)

// DNSPluginName is the key DNS providers are served and dispensed under.
const DNSPluginName = "dns"

// Record types RackBill writes.
const (
	RecordPTR   = "PTR"
	RecordA     = "A"
	RecordAAAA  = "AAAA"
	RecordCNAME = "CNAME"
	RecordTXT   = "TXT"
)

// DNS record operation statuses.
const (
	DNSSucceeded = "succeeded"
	DNSFailed    = "failed"
)

type DNSProviderInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Version     string `json:"version"`
	Maturity    string `json:"maturity"` // Maturity* level (maturity.go)
	// Capability flags (§5): the host only routes reverse-DNS work to
	// providers declaring SupportsPTR.
	SupportsPTR     bool          `json:"supports_ptr"`
	SupportsForward bool          `json:"supports_forward"`
	ConfigFields    []ConfigField `json:"config_fields"`
}

// RecordInput identifies one record. Zone and Name carry no trailing dot;
// providers that need one add it.
type RecordInput struct {
	TenantID   string
	Zone       string
	Name       string
	Type       string
	Value      string
	TTLSeconds int
	Config     map[string]string
}

type DNSResult struct {
	Status        string
	FailureReason string
}

// DNSProvider is what a DNS plugin implements. Upsert replaces the record
// set at (zone, name, type) with one value; Delete removes it and treats
// "already absent" as success — the host retries, and only the end state
// matters.
type DNSProvider interface {
	Describe(ctx context.Context) (DNSProviderInfo, error)
	UpsertRecord(ctx context.Context, in RecordInput) (DNSResult, error)
	DeleteRecord(ctx context.Context, in RecordInput) (DNSResult, error)
}

// DNSPlugin adapts a DNSProvider across the go-plugin gRPC boundary.
type DNSPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl DNSProvider
}

func (p *DNSPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterDNSProviderServer(s, &dnsGRPCServer{impl: p.Impl})
	return nil
}

func (p *DNSPlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &dnsGRPCClient{client: proto.NewDNSProviderClient(c)}, nil
}

// ServeDNSProvider is the entry point for DNS plugin binaries.
func ServeDNSProvider(impl DNSProvider) { ServeBundle(Bundle{DNSProvider: impl}) }

// --- gRPC adapters ---

type dnsGRPCServer struct {
	proto.UnimplementedDNSProviderServer
	impl DNSProvider
}

func (s *dnsGRPCServer) DescribeDNSProvider(ctx context.Context, _ *proto.DescribeDNSProviderRequest) (*proto.DescribeDNSProviderResponse, error) {
	info, err := s.impl.Describe(ctx)
	if err != nil {
		return nil, err
	}
	fields := configFieldsToProto(info.ConfigFields)
	return &proto.DescribeDNSProviderResponse{
		Name: info.Name, DisplayName: info.DisplayName, Version: info.Version, Maturity: info.Maturity,
		SupportsPtr: info.SupportsPTR, SupportsForward: info.SupportsForward,
		ConfigFields: fields,
	}, nil
}

func (s *dnsGRPCServer) UpsertRecord(ctx context.Context, req *proto.UpsertRecordRequest) (*proto.DNSRecordResponse, error) {
	res, err := s.impl.UpsertRecord(ctx, RecordInput{
		TenantID: req.TenantId, Zone: req.Zone, Name: req.Name, Type: req.Type,
		Value: req.Value, TTLSeconds: int(req.TtlSeconds), Config: req.Config,
	})
	if err != nil {
		return nil, err
	}
	return &proto.DNSRecordResponse{Status: res.Status, FailureReason: res.FailureReason}, nil
}

func (s *dnsGRPCServer) DeleteRecord(ctx context.Context, req *proto.DeleteRecordRequest) (*proto.DNSRecordResponse, error) {
	res, err := s.impl.DeleteRecord(ctx, RecordInput{
		TenantID: req.TenantId, Zone: req.Zone, Name: req.Name, Type: req.Type, Config: req.Config,
	})
	if err != nil {
		return nil, err
	}
	return &proto.DNSRecordResponse{Status: res.Status, FailureReason: res.FailureReason}, nil
}

type dnsGRPCClient struct {
	client proto.DNSProviderClient
}

func (c *dnsGRPCClient) Describe(ctx context.Context) (DNSProviderInfo, error) {
	resp, err := c.client.DescribeDNSProvider(ctx, &proto.DescribeDNSProviderRequest{})
	if err != nil {
		return DNSProviderInfo{}, err
	}
	fields := configFieldsFromProto(resp.ConfigFields)
	return DNSProviderInfo{
		Name: resp.Name, DisplayName: resp.DisplayName, Version: resp.Version, Maturity: resp.Maturity,
		SupportsPTR: resp.SupportsPtr, SupportsForward: resp.SupportsForward,
		ConfigFields: fields,
	}, nil
}

func (c *dnsGRPCClient) UpsertRecord(ctx context.Context, in RecordInput) (DNSResult, error) {
	resp, err := c.client.UpsertRecord(ctx, &proto.UpsertRecordRequest{
		TenantId: in.TenantID, Zone: in.Zone, Name: in.Name, Type: in.Type,
		Value: in.Value, TtlSeconds: int32(in.TTLSeconds), Config: in.Config,
	})
	if err != nil {
		return DNSResult{}, err
	}
	return DNSResult{Status: resp.Status, FailureReason: resp.FailureReason}, nil
}

func (c *dnsGRPCClient) DeleteRecord(ctx context.Context, in RecordInput) (DNSResult, error) {
	resp, err := c.client.DeleteRecord(ctx, &proto.DeleteRecordRequest{
		TenantId: in.TenantID, Zone: in.Zone, Name: in.Name, Type: in.Type, Config: in.Config,
	})
	if err != nil {
		return DNSResult{}, err
	}
	return DNSResult{Status: resp.Status, FailureReason: resp.FailureReason}, nil
}
