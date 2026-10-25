package plugin

import (
	"context"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"rackbill.com/sdk/plugin/proto"
)

// UsageSourcePluginName is the key usage sources are served and dispensed under.
const UsageSourcePluginName = "usage"

// MetricDescriptor advertises one metric a usage source can report; the admin
// meter editor offers these as known metric names.
type MetricDescriptor struct {
	Metric      string `json:"metric"`
	Unit        string `json:"unit"`
	Description string `json:"description"`
}

type UsageSourceInfo struct {
	Name         string             `json:"name"`
	DisplayName  string             `json:"display_name"`
	Version      string             `json:"version"`
	Maturity     string             `json:"maturity"` // Maturity* level (maturity.go)
	ConfigFields []ConfigField      `json:"config_fields"`
	Metrics      []MetricDescriptor `json:"metrics"`
}

// CollectInput identifies a service and the half-open window [WindowStart,
// WindowEnd) to report consumption for. Windows never overlap for a service.
type CollectInput struct {
	TenantID    string
	ServiceID   string
	ExternalRef string
	Config      map[string]string
	WindowStart time.Time
	WindowEnd   time.Time
}

// UsageRecord is consumption of one metric during the collected window —
// integrated quantities (bytes transferred, GiB-hours), never gauges.
type UsageRecord struct {
	Metric   string  `json:"metric"`
	Quantity float64 `json:"quantity"`
	Unit     string  `json:"unit"`
}

// UsageSource is what a metered-billing plugin implements. Collect failures
// are plain errors: the host logs, skips the window, and re-collects the whole
// span next cycle — never block billing on a usage source.
type UsageSource interface {
	Describe(ctx context.Context) (UsageSourceInfo, error)
	Collect(ctx context.Context, in CollectInput) ([]UsageRecord, error)
}

// UsageSourcePlugin adapts a UsageSource across the go-plugin gRPC boundary.
type UsageSourcePlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl UsageSource
}

func (p *UsageSourcePlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterUsageSourceServer(s, &usageGRPCServer{impl: p.Impl})
	return nil
}

func (p *UsageSourcePlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &usageGRPCClient{client: proto.NewUsageSourceClient(c)}, nil
}

// UsageSourceClient adapts a raw gRPC client into the UsageSource interface —
// the host's satellite-tunnel counterpart of the go-plugin dial.
func UsageSourceClient(c proto.UsageSourceClient) UsageSource {
	return &usageGRPCClient{client: c}
}

// UsageSourceServer adapts a UsageSource into the wire-facing gRPC server for
// the satellite relay.
func UsageSourceServer(impl UsageSource) proto.UsageSourceServer {
	return &usageGRPCServer{impl: impl}
}

// ServeUsageSource is the entry point for usage source plugin binaries.
func ServeUsageSource(impl UsageSource) { ServeBundle(Bundle{UsageSource: impl}) }

// --- gRPC adapters ---

type usageGRPCServer struct {
	proto.UnimplementedUsageSourceServer
	impl UsageSource
}

func (s *usageGRPCServer) DescribeUsageSource(ctx context.Context, _ *proto.DescribeUsageSourceRequest) (*proto.DescribeUsageSourceResponse, error) {
	info, err := s.impl.Describe(ctx)
	if err != nil {
		return nil, err
	}
	fields := configFieldsToProto(info.ConfigFields)
	metrics := make([]*proto.MetricDescriptor, len(info.Metrics))
	for i, m := range info.Metrics {
		metrics[i] = &proto.MetricDescriptor{Metric: m.Metric, Unit: m.Unit, Description: m.Description}
	}
	return &proto.DescribeUsageSourceResponse{
		Name: info.Name, DisplayName: info.DisplayName, Version: info.Version, Maturity: info.Maturity,
		ConfigFields: fields, Metrics: metrics,
	}, nil
}

func (s *usageGRPCServer) Collect(ctx context.Context, req *proto.CollectRequest) (*proto.CollectResponse, error) {
	records, err := s.impl.Collect(ctx, CollectInput{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef,
		Config:      req.Config,
		WindowStart: time.Unix(req.WindowStartUnix, 0).UTC(),
		WindowEnd:   time.Unix(req.WindowEndUnix, 0).UTC(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*proto.UsageRecord, len(records))
	for i, r := range records {
		out[i] = &proto.UsageRecord{Metric: r.Metric, Quantity: r.Quantity, Unit: r.Unit}
	}
	return &proto.CollectResponse{Records: out}, nil
}

type usageGRPCClient struct {
	client proto.UsageSourceClient
}

func (c *usageGRPCClient) Describe(ctx context.Context) (UsageSourceInfo, error) {
	resp, err := c.client.DescribeUsageSource(ctx, &proto.DescribeUsageSourceRequest{})
	if err != nil {
		return UsageSourceInfo{}, err
	}
	fields := configFieldsFromProto(resp.ConfigFields)
	metrics := make([]MetricDescriptor, len(resp.Metrics))
	for i, m := range resp.Metrics {
		metrics[i] = MetricDescriptor{Metric: m.Metric, Unit: m.Unit, Description: m.Description}
	}
	return UsageSourceInfo{
		Name: resp.Name, DisplayName: resp.DisplayName, Version: resp.Version, Maturity: resp.Maturity,
		ConfigFields: fields, Metrics: metrics,
	}, nil
}

func (c *usageGRPCClient) Collect(ctx context.Context, in CollectInput) ([]UsageRecord, error) {
	resp, err := c.client.Collect(ctx, &proto.CollectRequest{
		TenantId: in.TenantID, ServiceId: in.ServiceID, ExternalRef: in.ExternalRef,
		Config:          in.Config,
		WindowStartUnix: in.WindowStart.Unix(),
		WindowEndUnix:   in.WindowEnd.Unix(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]UsageRecord, len(resp.Records))
	for i, r := range resp.Records {
		out[i] = UsageRecord{Metric: r.Metric, Quantity: r.Quantity, Unit: r.Unit}
	}
	return out, nil
}
