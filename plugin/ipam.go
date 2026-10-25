package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"rackbill.com/sdk/plugin/proto"
)

// IPAMPluginName is the key upstream IPAM providers are served and dispensed
// under.
const IPAMPluginName = "ipam"

// Prefix statuses an IPAM reports. Container prefixes hold other prefixes,
// not hosts, and are never importable.
const (
	IPAMPrefixActive     = "active"
	IPAMPrefixReserved   = "reserved"
	IPAMPrefixDeprecated = "deprecated"
	IPAMPrefixContainer  = "container"
)

// Address statuses. Active and reserved are the two RackBill writes; the
// rest are what an IPAM may report for addresses held by others. Any
// recorded address that is not RackBill's own is mirrored as a reservation
// whatever its status says — the IPAM is the arbiter of "taken".
const (
	IPAMAddressActive     = "active"
	IPAMAddressReserved   = "reserved"
	IPAMAddressDeprecated = "deprecated"
	IPAMAddressDHCP       = "dhcp"
)

// Outcomes of allocate/update/release.
const (
	IPAMSucceeded = "succeeded"
	IPAMConflict  = "conflict" // allocate only: the IPAM holds the address for someone else
	IPAMFailed    = "failed"
)

type IPAMProviderInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Version     string `json:"version"`
	Maturity    string `json:"maturity"` // Maturity* level (maturity.go)
	// ConfigFields are the tenant-level settings the admin IPAM card renders:
	// the API URL, credentials, and whatever filter narrows the prefixes the
	// IPAM exposes to RackBill (a tag, a role, a section). Secret-typed values
	// are sealed at rest by the agent and opened per call.
	ConfigFields []ConfigField `json:"config_fields"`
}

// IPAMPrefix is one prefix as the IPAM records it. Ref is the IPAM's own
// identifier for the object and is what the agent stores on the linked block
// and sends back on every later call.
type IPAMPrefix struct {
	Ref         string   `json:"ref"`
	CIDR        string   `json:"cidr"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Gateway     string   `json:"gateway"`
	VRF         string   `json:"vrf"`
	Tags        []string `json:"tags"`
	Importable  bool     `json:"importable"`
}

// IPAMAddress is one recorded address. Holder is a free-text rendering of
// who has it, used verbatim in the agent's reservation note when the address
// is not RackBill's; RackBill reports whether the record carries RackBill's
// own marker so the agent recognises its allocations even without a stored
// ref.
type IPAMAddress struct {
	Ref         string   `json:"ref"`
	Address     string   `json:"address"`
	Status      string   `json:"status"`
	Hostname    string   `json:"hostname"`
	Description string   `json:"description"`
	Holder      string   `json:"holder"`
	Tags        []string `json:"tags"`
	RackBill    bool     `json:"rackbill"`
}

// IPAMPrefixQuery scopes a per-prefix call. PrefixRef is the IPAM's id for
// the prefix; CIDR rides alongside for IPAMs that filter by network.
type IPAMPrefixQuery struct {
	TenantID  string
	Config    map[string]string
	PrefixRef string
	CIDR      string
}

// IPAMAllocation names the address the agent claimed and how to record it.
type IPAMAllocation struct {
	TenantID    string
	Config      map[string]string
	PrefixRef   string
	CIDR        string
	Address     string
	Status      string // IPAMAddressActive or IPAMAddressReserved
	Hostname    string
	Description string
}

// IPAMUpdate rewrites the mutable fields of an address RackBill holds.
type IPAMUpdate struct {
	TenantID    string
	Config      map[string]string
	Ref         string
	Address     string
	Status      string
	Hostname    string
	Description string
}

// IPAMRelease identifies an address RackBill holds and no longer needs.
type IPAMRelease struct {
	TenantID  string
	Config    map[string]string
	Ref       string
	Address   string
	PrefixRef string
}

// IPAMAddressResult answers allocate and update. Status is one of the IPAM*
// outcomes; Address carries the recorded object on success; Holder says who
// has the address on a conflict.
type IPAMAddressResult struct {
	Status        string
	FailureReason string
	Address       IPAMAddress
	Holder        string
}

type IPAMReleaseResult struct {
	Status        string
	FailureReason string
}

// IPAMProvider is what an upstream-IPAM plugin implements. Listing calls are
// interactive (an admin button, seconds-scale budgets); allocate, update and
// release are driven from the agent's retrying outbox, so a transport error
// is retried by the host and must not be retried inside the plugin.
//
// ListAddresses returns the addresses the IPAM has *recorded* inside a
// prefix, never an enumeration of the prefix: an absent address is free
// upstream. Allocate names a specific address; an address the IPAM already
// holds for someone else is a declared IPAMConflict naming the holder, one
// it already holds for RackBill (a retry) is success returning the existing
// object. Release of an address the IPAM no longer holds is success.
type IPAMProvider interface {
	Describe(ctx context.Context) (IPAMProviderInfo, error)
	ListPrefixes(ctx context.Context, tenantID string, config map[string]string) ([]IPAMPrefix, error)
	ListAddresses(ctx context.Context, q IPAMPrefixQuery) ([]IPAMAddress, error)
	AllocateAddress(ctx context.Context, in IPAMAllocation) (IPAMAddressResult, error)
	UpdateAddress(ctx context.Context, in IPAMUpdate) (IPAMAddressResult, error)
	ReleaseAddress(ctx context.Context, in IPAMRelease) (IPAMReleaseResult, error)
}

// IPAMPlugin adapts an IPAMProvider across the go-plugin gRPC boundary.
type IPAMPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl IPAMProvider
}

func (p *IPAMPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterIPAMProviderServer(s, &ipamGRPCServer{impl: p.Impl})
	return nil
}

func (p *IPAMPlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &ipamGRPCClient{client: proto.NewIPAMProviderClient(c)}, nil
}

// ServeIPAMProvider is the entry point for upstream-IPAM plugin binaries.
func ServeIPAMProvider(impl IPAMProvider) { ServeBundle(Bundle{IPAMProvider: impl}) }

// --- wire conversions ---

func ipamPrefixToProto(p IPAMPrefix) *proto.IPAMPrefix {
	return &proto.IPAMPrefix{
		Ref: p.Ref, Cidr: p.CIDR, Description: p.Description, Status: p.Status,
		Gateway: p.Gateway, Vrf: p.VRF, Tags: p.Tags, Importable: p.Importable,
	}
}

func ipamPrefixFromProto(p *proto.IPAMPrefix) IPAMPrefix {
	if p == nil {
		return IPAMPrefix{}
	}
	return IPAMPrefix{
		Ref: p.Ref, CIDR: p.Cidr, Description: p.Description, Status: p.Status,
		Gateway: p.Gateway, VRF: p.Vrf, Tags: p.Tags, Importable: p.Importable,
	}
}

func ipamAddressToProto(a IPAMAddress) *proto.IPAMAddress {
	return &proto.IPAMAddress{
		Ref: a.Ref, Address: a.Address, Status: a.Status, Hostname: a.Hostname,
		Description: a.Description, Holder: a.Holder, Tags: a.Tags, Rackbill: a.RackBill,
	}
}

func ipamAddressFromProto(a *proto.IPAMAddress) IPAMAddress {
	if a == nil {
		return IPAMAddress{}
	}
	return IPAMAddress{
		Ref: a.Ref, Address: a.Address, Status: a.Status, Hostname: a.Hostname,
		Description: a.Description, Holder: a.Holder, Tags: a.Tags, RackBill: a.Rackbill,
	}
}

func ipamAddressResultToProto(r IPAMAddressResult) *proto.IPAMAddressResponse {
	out := &proto.IPAMAddressResponse{Status: r.Status, FailureReason: r.FailureReason, Holder: r.Holder}
	if r.Status == IPAMSucceeded {
		out.Address = ipamAddressToProto(r.Address)
	}
	return out
}

func ipamAddressResultFromProto(r *proto.IPAMAddressResponse) IPAMAddressResult {
	return IPAMAddressResult{
		Status: r.Status, FailureReason: r.FailureReason, Holder: r.Holder,
		Address: ipamAddressFromProto(r.Address),
	}
}

// --- gRPC adapters ---

type ipamGRPCServer struct {
	proto.UnimplementedIPAMProviderServer
	impl IPAMProvider
}

func (s *ipamGRPCServer) DescribeIPAMProvider(ctx context.Context, _ *proto.DescribeIPAMProviderRequest) (*proto.DescribeIPAMProviderResponse, error) {
	info, err := s.impl.Describe(ctx)
	if err != nil {
		return nil, err
	}
	fields := configFieldsToProto(info.ConfigFields)
	return &proto.DescribeIPAMProviderResponse{
		Name: info.Name, DisplayName: info.DisplayName, Version: info.Version, Maturity: info.Maturity,
		ConfigFields: fields,
	}, nil
}

func (s *ipamGRPCServer) ListIPAMPrefixes(ctx context.Context, req *proto.ListIPAMPrefixesRequest) (*proto.ListIPAMPrefixesResponse, error) {
	prefixes, err := s.impl.ListPrefixes(ctx, req.TenantId, req.Config)
	if err != nil {
		return nil, err
	}
	out := &proto.ListIPAMPrefixesResponse{Prefixes: make([]*proto.IPAMPrefix, len(prefixes))}
	for i, p := range prefixes {
		out.Prefixes[i] = ipamPrefixToProto(p)
	}
	return out, nil
}

func (s *ipamGRPCServer) ListIPAMAddresses(ctx context.Context, req *proto.ListIPAMAddressesRequest) (*proto.ListIPAMAddressesResponse, error) {
	addrs, err := s.impl.ListAddresses(ctx, IPAMPrefixQuery{
		TenantID: req.TenantId, Config: req.Config, PrefixRef: req.PrefixRef, CIDR: req.Cidr,
	})
	if err != nil {
		return nil, err
	}
	out := &proto.ListIPAMAddressesResponse{Addresses: make([]*proto.IPAMAddress, len(addrs))}
	for i, a := range addrs {
		out.Addresses[i] = ipamAddressToProto(a)
	}
	return out, nil
}

func (s *ipamGRPCServer) AllocateIPAMAddress(ctx context.Context, req *proto.AllocateIPAMAddressRequest) (*proto.IPAMAddressResponse, error) {
	res, err := s.impl.AllocateAddress(ctx, IPAMAllocation{
		TenantID: req.TenantId, Config: req.Config, PrefixRef: req.PrefixRef, CIDR: req.Cidr,
		Address: req.Address, Status: req.Status, Hostname: req.Hostname, Description: req.Description,
	})
	if err != nil {
		return nil, err
	}
	return ipamAddressResultToProto(res), nil
}

func (s *ipamGRPCServer) UpdateIPAMAddress(ctx context.Context, req *proto.UpdateIPAMAddressRequest) (*proto.IPAMAddressResponse, error) {
	res, err := s.impl.UpdateAddress(ctx, IPAMUpdate{
		TenantID: req.TenantId, Config: req.Config, Ref: req.Ref, Address: req.Address,
		Status: req.Status, Hostname: req.Hostname, Description: req.Description,
	})
	if err != nil {
		return nil, err
	}
	return ipamAddressResultToProto(res), nil
}

func (s *ipamGRPCServer) ReleaseIPAMAddress(ctx context.Context, req *proto.ReleaseIPAMAddressRequest) (*proto.IPAMReleaseResponse, error) {
	res, err := s.impl.ReleaseAddress(ctx, IPAMRelease{
		TenantID: req.TenantId, Config: req.Config, Ref: req.Ref, Address: req.Address, PrefixRef: req.PrefixRef,
	})
	if err != nil {
		return nil, err
	}
	return &proto.IPAMReleaseResponse{Status: res.Status, FailureReason: res.FailureReason}, nil
}

type ipamGRPCClient struct {
	client proto.IPAMProviderClient
}

func (c *ipamGRPCClient) Describe(ctx context.Context) (IPAMProviderInfo, error) {
	resp, err := c.client.DescribeIPAMProvider(ctx, &proto.DescribeIPAMProviderRequest{})
	if err != nil {
		return IPAMProviderInfo{}, err
	}
	fields := configFieldsFromProto(resp.ConfigFields)
	return IPAMProviderInfo{
		Name: resp.Name, DisplayName: resp.DisplayName, Version: resp.Version, Maturity: resp.Maturity,
		ConfigFields: fields,
	}, nil
}

func (c *ipamGRPCClient) ListPrefixes(ctx context.Context, tenantID string, config map[string]string) ([]IPAMPrefix, error) {
	resp, err := c.client.ListIPAMPrefixes(ctx, &proto.ListIPAMPrefixesRequest{TenantId: tenantID, Config: config})
	if err != nil {
		return nil, err
	}
	out := make([]IPAMPrefix, len(resp.Prefixes))
	for i, p := range resp.Prefixes {
		out[i] = ipamPrefixFromProto(p)
	}
	return out, nil
}

func (c *ipamGRPCClient) ListAddresses(ctx context.Context, q IPAMPrefixQuery) ([]IPAMAddress, error) {
	resp, err := c.client.ListIPAMAddresses(ctx, &proto.ListIPAMAddressesRequest{
		TenantId: q.TenantID, Config: q.Config, PrefixRef: q.PrefixRef, Cidr: q.CIDR,
	})
	if err != nil {
		return nil, err
	}
	out := make([]IPAMAddress, len(resp.Addresses))
	for i, a := range resp.Addresses {
		out[i] = ipamAddressFromProto(a)
	}
	return out, nil
}

func (c *ipamGRPCClient) AllocateAddress(ctx context.Context, in IPAMAllocation) (IPAMAddressResult, error) {
	resp, err := c.client.AllocateIPAMAddress(ctx, &proto.AllocateIPAMAddressRequest{
		TenantId: in.TenantID, Config: in.Config, PrefixRef: in.PrefixRef, Cidr: in.CIDR,
		Address: in.Address, Status: in.Status, Hostname: in.Hostname, Description: in.Description,
	})
	if err != nil {
		return IPAMAddressResult{}, err
	}
	return ipamAddressResultFromProto(resp), nil
}

func (c *ipamGRPCClient) UpdateAddress(ctx context.Context, in IPAMUpdate) (IPAMAddressResult, error) {
	resp, err := c.client.UpdateIPAMAddress(ctx, &proto.UpdateIPAMAddressRequest{
		TenantId: in.TenantID, Config: in.Config, Ref: in.Ref, Address: in.Address,
		Status: in.Status, Hostname: in.Hostname, Description: in.Description,
	})
	if err != nil {
		return IPAMAddressResult{}, err
	}
	return ipamAddressResultFromProto(resp), nil
}

func (c *ipamGRPCClient) ReleaseAddress(ctx context.Context, in IPAMRelease) (IPAMReleaseResult, error) {
	resp, err := c.client.ReleaseIPAMAddress(ctx, &proto.ReleaseIPAMAddressRequest{
		TenantId: in.TenantID, Config: in.Config, Ref: in.Ref, Address: in.Address, PrefixRef: in.PrefixRef,
	})
	if err != nil {
		return IPAMReleaseResult{}, err
	}
	return IPAMReleaseResult{Status: resp.Status, FailureReason: resp.FailureReason}, nil
}
