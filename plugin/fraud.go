package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"rackbill.com/sdk/plugin/proto"
)

// FraudPluginName is the key fraud screens are served and dispensed under.
const FraudPluginName = "fraud"

// Fraud verdicts.
const (
	VerdictAllow  = "allow"
	VerdictReview = "review"
	VerdictDeny   = "deny"
)

type FraudScreenInfo struct {
	Name         string        `json:"name"`
	DisplayName  string        `json:"display_name"`
	Version      string        `json:"version"`
	Maturity     string        `json:"maturity"` // Maturity* level (maturity.go)
	ConfigFields []ConfigField `json:"config_fields"`
}

// ScoreInput is the order context at checkout. Zero values mean "unknown";
// plugins score with what they get.
type ScoreInput struct {
	TenantID       string
	AccountID      string
	Email          string
	IP             string
	Country        string
	Region         string
	ProductName    string
	AmountCents    int64
	Currency       string
	Domain         string
	AccountAgeDays int
	Config         map[string]string
}

// ScoreResult carries the plugin's judgement. Verdict mapping (thresholds)
// is the plugin's job; the host only routes on the verdict.
type ScoreResult struct {
	Score   int      `json:"score"` // 0 clean … 100 certain fraud
	Verdict string   `json:"verdict"`
	Reasons []string `json:"reasons"`
}

// FraudScreen is what an order-scoring plugin implements. Score runs inline
// during checkout under a hard 5s budget; a returned error means the host
// applies the tenant's configured outage policy (review by default, block
// for operators selling fraud-prone products).
type FraudScreen interface {
	Describe(ctx context.Context) (FraudScreenInfo, error)
	Score(ctx context.Context, in ScoreInput) (ScoreResult, error)
}

// FraudPlugin adapts a FraudScreen across the go-plugin gRPC boundary.
type FraudPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl FraudScreen
}

func (p *FraudPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterFraudScreenServer(s, &fraudGRPCServer{impl: p.Impl})
	return nil
}

func (p *FraudPlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &fraudGRPCClient{client: proto.NewFraudScreenClient(c)}, nil
}

// ServeFraudScreen is the entry point for fraud screen plugin binaries.
func ServeFraudScreen(impl FraudScreen) { ServeBundle(Bundle{FraudScreen: impl}) }

// --- gRPC adapters ---

type fraudGRPCServer struct {
	proto.UnimplementedFraudScreenServer
	impl FraudScreen
}

func (s *fraudGRPCServer) DescribeFraudScreen(ctx context.Context, _ *proto.DescribeFraudScreenRequest) (*proto.DescribeFraudScreenResponse, error) {
	info, err := s.impl.Describe(ctx)
	if err != nil {
		return nil, err
	}
	fields := configFieldsToProto(info.ConfigFields)
	return &proto.DescribeFraudScreenResponse{
		Name: info.Name, DisplayName: info.DisplayName, Version: info.Version, Maturity: info.Maturity,
		ConfigFields: fields,
	}, nil
}

func (s *fraudGRPCServer) Score(ctx context.Context, req *proto.ScoreRequest) (*proto.ScoreResponse, error) {
	res, err := s.impl.Score(ctx, ScoreInput{
		TenantID: req.TenantId, AccountID: req.AccountId, Email: req.Email, IP: req.Ip,
		Country: req.Country, Region: req.Region, ProductName: req.ProductName,
		AmountCents: req.AmountCents, Currency: req.Currency, Domain: req.Domain,
		AccountAgeDays: int(req.AccountAgeDays), Config: req.Config,
	})
	if err != nil {
		return nil, err
	}
	return &proto.ScoreResponse{Score: int32(res.Score), Verdict: res.Verdict, Reasons: res.Reasons}, nil
}

type fraudGRPCClient struct {
	client proto.FraudScreenClient
}

func (c *fraudGRPCClient) Describe(ctx context.Context) (FraudScreenInfo, error) {
	resp, err := c.client.DescribeFraudScreen(ctx, &proto.DescribeFraudScreenRequest{})
	if err != nil {
		return FraudScreenInfo{}, err
	}
	fields := configFieldsFromProto(resp.ConfigFields)
	return FraudScreenInfo{
		Name: resp.Name, DisplayName: resp.DisplayName, Version: resp.Version, Maturity: resp.Maturity,
		ConfigFields: fields,
	}, nil
}

func (c *fraudGRPCClient) Score(ctx context.Context, in ScoreInput) (ScoreResult, error) {
	resp, err := c.client.Score(ctx, &proto.ScoreRequest{
		TenantId: in.TenantID, AccountId: in.AccountID, Email: in.Email, Ip: in.IP,
		Country: in.Country, Region: in.Region, ProductName: in.ProductName,
		AmountCents: in.AmountCents, Currency: in.Currency, Domain: in.Domain,
		AccountAgeDays: int32(in.AccountAgeDays), Config: in.Config,
	})
	if err != nil {
		return ScoreResult{}, err
	}
	return ScoreResult{Score: int(resp.Score), Verdict: resp.Verdict, Reasons: resp.Reasons}, nil
}
