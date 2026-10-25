package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"rackbill.com/sdk/plugin/proto"
)

// NotifierPluginName is the key notifiers are served and dispensed under.
const NotifierPluginName = "notifier"

// Notification channels.
const (
	ChannelEmail = "email"
	ChannelSMS   = "sms"
	// ChannelChat delivers to a team-chat platform (§9.2): To is the
	// platform's channel id, Body is fully rendered text.
	ChannelChat = "chat"
)

// Send statuses.
const (
	SendSent   = "sent"
	SendFailed = "failed"
)

// Member-role change statuses.
const (
	RolesSucceeded = "succeeded"
	RolesFailed    = "failed"
)

type NotifierInfo struct {
	Name         string        `json:"name"`
	DisplayName  string        `json:"display_name"`
	Version      string        `json:"version"`
	Maturity     string        `json:"maturity"` // Maturity* level (maturity.go)
	Channels     []string      `json:"channels"`
	ConfigFields []ConfigField `json:"config_fields"`
	// SupportsMemberRoles gates GetChatMember / ModifyChatMemberRoles (§9.2):
	// the chat platform can grant paid subscribers roles.
	SupportsMemberRoles bool `json:"supports_member_roles"`
	// IdentityProvider is the login-provider machine name whose
	// provider_user_id values are this chat platform's user ids ("discord").
	// Empty when the notifier has no identity story (mail, sms).
	IdentityProvider string `json:"identity_provider,omitempty"`
}

// SendInput is one fully rendered message. The agent owns templating and the
// message log; Config carries the tenant's own delivery settings (their relay,
// their credentials) on every call.
type SendInput struct {
	TenantID string
	Channel  string
	To       string
	Subject  string // empty for channels without subjects
	Body     string
	// HTMLBody is the optional branded HTML alternative for email. Plugins
	// that can send multipart include it; everyone else sends Body alone —
	// the text part is always complete on its own.
	HTMLBody string
	Config   map[string]string
}

type SendResult struct {
	Status        string // SendSent, SendFailed
	ProviderID    string
	FailureReason string
}

// Notifier is what a delivery plugin implements. A returned error means
// "couldn't reach the provider"; a declared failed status means the provider
// rejected the message — both count as failed attempts.
type Notifier interface {
	Describe(ctx context.Context) (NotifierInfo, error)
	Send(ctx context.Context, in SendInput) (SendResult, error)
}

// ChatMember is one member of the tenant's chat server as the platform
// reports them right now.
type ChatMember struct {
	Found       bool     // false = not a member of the server; not an error
	RoleIDs     []string // current role ids
	DisplayName string
}

// RoleChangeResult reports one member's role modification.
type RoleChangeResult struct {
	Status        string // RolesSucceeded, RolesFailed
	FailureReason string
}

// ChatMemberRoler is the optional member-roles capability of a notifier
// (§9.2). Implement it alongside Notifier and set SupportsMemberRoles in
// Describe. The agent computes the desired role set; the plugin only reads
// one member and applies adds/removes. userID is the platform's own user id.
type ChatMemberRoler interface {
	GetChatMember(ctx context.Context, tenantID, userID string, config map[string]string) (ChatMember, error)
	ModifyChatMemberRoles(ctx context.Context, tenantID, userID string, add, remove []string, config map[string]string) (RoleChangeResult, error)
}

// NotifierPlugin adapts a Notifier across the go-plugin gRPC boundary.
type NotifierPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl Notifier
}

func (p *NotifierPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterNotifierServer(s, &notifierGRPCServer{impl: p.Impl})
	return nil
}

func (p *NotifierPlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &notifierGRPCClient{client: proto.NewNotifierClient(c)}, nil
}

// ServeNotifier is the entry point for notifier plugin binaries.
func ServeNotifier(impl Notifier) { ServeBundle(Bundle{Notifier: impl}) }

// --- gRPC adapters ---

type notifierGRPCServer struct {
	proto.UnimplementedNotifierServer
	impl Notifier
}

func (s *notifierGRPCServer) DescribeNotifier(ctx context.Context, _ *proto.DescribeNotifierRequest) (*proto.DescribeNotifierResponse, error) {
	info, err := s.impl.Describe(ctx)
	if err != nil {
		return nil, err
	}
	fields := configFieldsToProto(info.ConfigFields)
	return &proto.DescribeNotifierResponse{
		Name: info.Name, DisplayName: info.DisplayName, Version: info.Version, Maturity: info.Maturity,
		Channels: info.Channels, ConfigFields: fields,
		SupportsMemberRoles: info.SupportsMemberRoles,
		IdentityProvider:    info.IdentityProvider,
	}, nil
}

func (s *notifierGRPCServer) Send(ctx context.Context, req *proto.SendRequest) (*proto.SendResponse, error) {
	res, err := s.impl.Send(ctx, SendInput{
		TenantID: req.TenantId, Channel: req.Channel, To: req.To,
		Subject: req.Subject, Body: req.Body, HTMLBody: req.HtmlBody, Config: req.Config,
	})
	if err != nil {
		return nil, err
	}
	return &proto.SendResponse{
		Status: res.Status, ProviderId: res.ProviderID, FailureReason: res.FailureReason,
	}, nil
}

func (s *notifierGRPCServer) ListConfigOptions(ctx context.Context, req *proto.ListConfigOptionsRequest) (*proto.ListConfigOptionsResponse, error) {
	m, ok := s.impl.(ConfigOptionLister)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement config-option enumeration")
	}
	opts, err := m.ListConfigOptions(ctx, req.TenantId, req.Key, req.Config)
	if err != nil {
		return nil, err
	}
	out := make([]*proto.ConfigFieldOption, len(opts))
	for i, o := range opts {
		out[i] = &proto.ConfigFieldOption{Value: o.Value, Label: o.Label}
	}
	return &proto.ListConfigOptionsResponse{Options: out}, nil
}

func (s *notifierGRPCServer) roles() (ChatMemberRoler, error) {
	m, ok := s.impl.(ChatMemberRoler)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the member-roles capability")
	}
	return m, nil
}

func (s *notifierGRPCServer) GetChatMember(ctx context.Context, req *proto.GetChatMemberRequest) (*proto.GetChatMemberResponse, error) {
	m, err := s.roles()
	if err != nil {
		return nil, err
	}
	member, err := m.GetChatMember(ctx, req.TenantId, req.UserId, req.Config)
	if err != nil {
		return nil, err
	}
	return &proto.GetChatMemberResponse{
		Found: member.Found, RoleIds: member.RoleIDs, DisplayName: member.DisplayName,
	}, nil
}

func (s *notifierGRPCServer) ModifyChatMemberRoles(ctx context.Context, req *proto.ModifyChatMemberRolesRequest) (*proto.ModifyChatMemberRolesResponse, error) {
	m, err := s.roles()
	if err != nil {
		return nil, err
	}
	res, err := m.ModifyChatMemberRoles(ctx, req.TenantId, req.UserId, req.AddRoleIds, req.RemoveRoleIds, req.Config)
	if err != nil {
		return nil, err
	}
	return &proto.ModifyChatMemberRolesResponse{Status: res.Status, FailureReason: res.FailureReason}, nil
}

type notifierGRPCClient struct {
	client proto.NotifierClient
}

func (c *notifierGRPCClient) Describe(ctx context.Context) (NotifierInfo, error) {
	resp, err := c.client.DescribeNotifier(ctx, &proto.DescribeNotifierRequest{})
	if err != nil {
		return NotifierInfo{}, err
	}
	fields := configFieldsFromProto(resp.ConfigFields)
	return NotifierInfo{
		Name: resp.Name, DisplayName: resp.DisplayName, Version: resp.Version, Maturity: resp.Maturity,
		Channels: resp.Channels, ConfigFields: fields,
		SupportsMemberRoles: resp.SupportsMemberRoles,
		IdentityProvider:    resp.IdentityProvider,
	}, nil
}

func (c *notifierGRPCClient) Send(ctx context.Context, in SendInput) (SendResult, error) {
	resp, err := c.client.Send(ctx, &proto.SendRequest{
		TenantId: in.TenantID, Channel: in.Channel, To: in.To,
		Subject: in.Subject, Body: in.Body, HtmlBody: in.HTMLBody, Config: in.Config,
	})
	if err != nil {
		return SendResult{}, err
	}
	return SendResult{
		Status: resp.Status, ProviderID: resp.ProviderId, FailureReason: resp.FailureReason,
	}, nil
}

// The client side always exposes ConfigOptionLister and ChatMemberRoler; the
// host only routes these calls per the Describe declarations.

func (c *notifierGRPCClient) ListConfigOptions(ctx context.Context, tenantID, key string, config map[string]string) ([]ConfigOption, error) {
	resp, err := c.client.ListConfigOptions(ctx, &proto.ListConfigOptionsRequest{
		TenantId: tenantID, Key: key, Config: config,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ConfigOption, len(resp.Options))
	for i, o := range resp.Options {
		out[i] = ConfigOption{Value: o.Value, Label: o.Label}
	}
	return out, nil
}

func (c *notifierGRPCClient) GetChatMember(ctx context.Context, tenantID, userID string, config map[string]string) (ChatMember, error) {
	resp, err := c.client.GetChatMember(ctx, &proto.GetChatMemberRequest{
		TenantId: tenantID, UserId: userID, Config: config,
	})
	if err != nil {
		return ChatMember{}, err
	}
	return ChatMember{Found: resp.Found, RoleIDs: resp.RoleIds, DisplayName: resp.DisplayName}, nil
}

func (c *notifierGRPCClient) ModifyChatMemberRoles(ctx context.Context, tenantID, userID string, add, remove []string, config map[string]string) (RoleChangeResult, error) {
	resp, err := c.client.ModifyChatMemberRoles(ctx, &proto.ModifyChatMemberRolesRequest{
		TenantId: tenantID, UserId: userID, AddRoleIds: add, RemoveRoleIds: remove, Config: config,
	})
	if err != nil {
		return RoleChangeResult{}, err
	}
	return RoleChangeResult{Status: resp.Status, FailureReason: resp.FailureReason}, nil
}
