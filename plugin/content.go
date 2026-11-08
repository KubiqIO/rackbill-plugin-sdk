package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"rackbill.com/sdk/plugin/proto"
)

// ContentPluginName is the dispense key and binary prefix for portal content
// providers. Name a standalone binary content-<name>.
const ContentPluginName = "content"

// ContentPack is a static translation of existing portal catalog keys. IDs
// are unique within one provider and match [a-z0-9][a-z0-9-]{0,62}. Locale
// must be supported by the host. Values use the catalog's ICU subset and
// declared placeholders; packs cannot create keys or reserved tenant content.
// Missing keys are allowed and fall back through the normal copy cascade.
type ContentPack struct {
	ID          string            `json:"id"`
	DisplayName string            `json:"display_name"`
	Locale      string            `json:"locale"`
	Values      map[string]string `json:"values"`
}

// ContentInfo is the offering snapshotted at plugin load. Providers offer
// 1–32 packs with at most 2 MiB of combined identity, key and value bytes.
// Display names are nonblank and at most 200 UTF-8 bytes. Pack contents are
// validated together: an invalid pack refuses the whole plugin registration.
type ContentInfo struct {
	Name        string        `json:"name"`
	DisplayName string        `json:"display_name"`
	Version     string        `json:"version"`
	Maturity    string        `json:"maturity"`
	Packs       []ContentPack `json:"packs"`
}

// ContentProvider distributes portal content packs. Describe must return a
// deterministic static offering and is called at load, relaunch and for
// -rb-plugin-info. Operators apply a pack to a tenant's draft and publish it
// through the normal content workflow. Updates do not overwrite tenant copy;
// disabling or removing a plugin does not remove previously applied content.
type ContentProvider interface {
	Describe(context.Context) (ContentInfo, error)
}

// ContentPlugin adapts a ContentProvider across the go-plugin gRPC boundary.
type ContentPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl ContentProvider
}

func (p *ContentPlugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterContentProviderServer(s, &contentGRPCServer{impl: p.Impl})
	return nil
}

func (p *ContentPlugin) GRPCClient(_ context.Context, _ *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &contentGRPCClient{client: proto.NewContentProviderClient(c)}, nil
}

// ServeContentProvider is the entry point for a portal content plugin.
func ServeContentProvider(impl ContentProvider) { ServeBundle(Bundle{ContentProvider: impl}) }

type contentGRPCServer struct {
	proto.UnimplementedContentProviderServer
	impl ContentProvider
}

func (s *contentGRPCServer) DescribeContentProvider(ctx context.Context, _ *proto.DescribeContentProviderRequest) (*proto.DescribeContentProviderResponse, error) {
	info, err := s.impl.Describe(ctx)
	if err != nil {
		return nil, err
	}
	packs := make([]*proto.ContentPack, 0, len(info.Packs))
	for _, p := range info.Packs {
		packs = append(packs, &proto.ContentPack{Id: p.ID, DisplayName: p.DisplayName, Locale: p.Locale, Values: p.Values})
	}
	return &proto.DescribeContentProviderResponse{
		Name: info.Name, DisplayName: info.DisplayName, Version: info.Version, Maturity: info.Maturity, Packs: packs,
	}, nil
}

type contentGRPCClient struct{ client proto.ContentProviderClient }

func (c *contentGRPCClient) Describe(ctx context.Context) (ContentInfo, error) {
	resp, err := c.client.DescribeContentProvider(ctx, &proto.DescribeContentProviderRequest{})
	if err != nil {
		return ContentInfo{}, err
	}
	packs := make([]ContentPack, 0, len(resp.Packs))
	for _, p := range resp.Packs {
		packs = append(packs, ContentPack{ID: p.GetId(), DisplayName: p.GetDisplayName(), Locale: p.GetLocale(), Values: p.GetValues()})
	}
	return ContentInfo{Name: resp.Name, DisplayName: resp.DisplayName, Version: resp.Version, Maturity: resp.Maturity, Packs: packs}, nil
}
