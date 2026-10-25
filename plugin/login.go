package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"kubiq.io/rackbill/sdk/plugin/proto"
)

// LoginPluginName is the key login providers are served and dispensed under.
const LoginPluginName = "login"

// Login completion statuses.
const (
	LoginSucceeded = "succeeded"
	LoginFailed    = "failed"
)

type LoginProviderInfo struct {
	Name         string        `json:"name"`
	DisplayName  string        `json:"display_name"`
	Version      string        `json:"version"`
	Maturity     string        `json:"maturity"` // Maturity* level (maturity.go)
	ConfigFields []ConfigField `json:"config_fields"`
	// IconSVG is the sign-in button's inline SVG, rendered verbatim by the
	// panel. Empty means a plain branded button.
	IconSVG string `json:"icon_svg"`
}

// BeginLoginInput carries the agent-minted state and PKCE challenge. Plugins
// whose provider supports PKCE include the challenge; others ignore it.
type BeginLoginInput struct {
	TenantID      string
	RedirectURI   string
	State         string
	CodeChallenge string // S256
	Config        map[string]string
}

// CompleteLoginInput resolves the authorization code from the callback.
type CompleteLoginInput struct {
	TenantID     string
	RedirectURI  string
	Code         string
	CodeVerifier string
	Config       map[string]string
}

// LoginIdentity is the resolved external identity. EmailVerified is the
// provider's own claim; the agent only matches existing users by email when
// it is true.
type LoginIdentity struct {
	Status         string `json:"status"` // "succeeded", "failed"
	FailureReason  string `json:"failure_reason"`
	ProviderUserID string `json:"provider_user_id"`
	Email          string `json:"email"`
	EmailVerified  bool   `json:"email_verified"`
	DisplayName    string `json:"display_name"`
	AvatarURL      string `json:"avatar_url"`
}

// LoginProvider is what an OAuth login plugin implements. Both calls are
// short interactive RPCs — a human is mid-redirect — so the host budgets
// seconds, not minutes. The agent owns state, PKCE, user matching, and
// sessions; the plugin owns its provider's protocol.
type LoginProvider interface {
	Describe(ctx context.Context) (LoginProviderInfo, error)
	BeginLogin(ctx context.Context, in BeginLoginInput) (string, error) // returns the auth URL
	CompleteLogin(ctx context.Context, in CompleteLoginInput) (LoginIdentity, error)
}

// LoginPlugin adapts a LoginProvider across the go-plugin gRPC boundary.
type LoginPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl LoginProvider
}

func (p *LoginPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterLoginProviderServer(s, &loginGRPCServer{impl: p.Impl})
	return nil
}

func (p *LoginPlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &loginGRPCClient{client: proto.NewLoginProviderClient(c)}, nil
}

// ServeLoginProvider is the entry point for login provider plugin binaries.
func ServeLoginProvider(impl LoginProvider) { ServeBundle(Bundle{LoginProvider: impl}) }

// --- gRPC adapters ---

type loginGRPCServer struct {
	proto.UnimplementedLoginProviderServer
	impl LoginProvider
}

func (s *loginGRPCServer) DescribeLoginProvider(ctx context.Context, _ *proto.DescribeLoginProviderRequest) (*proto.DescribeLoginProviderResponse, error) {
	info, err := s.impl.Describe(ctx)
	if err != nil {
		return nil, err
	}
	fields := configFieldsToProto(info.ConfigFields)
	return &proto.DescribeLoginProviderResponse{
		Name: info.Name, DisplayName: info.DisplayName, Version: info.Version, Maturity: info.Maturity,
		ConfigFields: fields, IconSvg: info.IconSVG,
	}, nil
}

func (s *loginGRPCServer) BeginLogin(ctx context.Context, req *proto.BeginLoginRequest) (*proto.BeginLoginResponse, error) {
	url, err := s.impl.BeginLogin(ctx, BeginLoginInput{
		TenantID: req.TenantId, RedirectURI: req.RedirectUri, State: req.State,
		CodeChallenge: req.CodeChallenge, Config: req.Config,
	})
	if err != nil {
		return nil, err
	}
	return &proto.BeginLoginResponse{AuthUrl: url}, nil
}

func (s *loginGRPCServer) CompleteLogin(ctx context.Context, req *proto.CompleteLoginRequest) (*proto.CompleteLoginResponse, error) {
	id, err := s.impl.CompleteLogin(ctx, CompleteLoginInput{
		TenantID: req.TenantId, RedirectURI: req.RedirectUri, Code: req.Code,
		CodeVerifier: req.CodeVerifier, Config: req.Config,
	})
	if err != nil {
		return nil, err
	}
	return &proto.CompleteLoginResponse{
		Status: id.Status, FailureReason: id.FailureReason,
		ProviderUserId: id.ProviderUserID, Email: id.Email, EmailVerified: id.EmailVerified,
		DisplayName: id.DisplayName, AvatarUrl: id.AvatarURL,
	}, nil
}

type loginGRPCClient struct {
	client proto.LoginProviderClient
}

func (c *loginGRPCClient) Describe(ctx context.Context) (LoginProviderInfo, error) {
	resp, err := c.client.DescribeLoginProvider(ctx, &proto.DescribeLoginProviderRequest{})
	if err != nil {
		return LoginProviderInfo{}, err
	}
	fields := configFieldsFromProto(resp.ConfigFields)
	return LoginProviderInfo{
		Name: resp.Name, DisplayName: resp.DisplayName, Version: resp.Version, Maturity: resp.Maturity,
		ConfigFields: fields, IconSVG: resp.IconSvg,
	}, nil
}

func (c *loginGRPCClient) BeginLogin(ctx context.Context, in BeginLoginInput) (string, error) {
	resp, err := c.client.BeginLogin(ctx, &proto.BeginLoginRequest{
		TenantId: in.TenantID, RedirectUri: in.RedirectURI, State: in.State,
		CodeChallenge: in.CodeChallenge, Config: in.Config,
	})
	if err != nil {
		return "", err
	}
	return resp.AuthUrl, nil
}

func (c *loginGRPCClient) CompleteLogin(ctx context.Context, in CompleteLoginInput) (LoginIdentity, error) {
	resp, err := c.client.CompleteLogin(ctx, &proto.CompleteLoginRequest{
		TenantId: in.TenantID, RedirectUri: in.RedirectURI, Code: in.Code,
		CodeVerifier: in.CodeVerifier, Config: in.Config,
	})
	if err != nil {
		return LoginIdentity{}, err
	}
	return LoginIdentity{
		Status: resp.Status, FailureReason: resp.FailureReason,
		ProviderUserID: resp.ProviderUserId, Email: resp.Email, EmailVerified: resp.EmailVerified,
		DisplayName: resp.DisplayName, AvatarURL: resp.AvatarUrl,
	}, nil
}
