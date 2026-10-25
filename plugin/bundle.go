package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"kubiq.io/rackbill/sdk/plugin/proto"
)

// Bundling: one binary, several kinds (plugin architecture §3, decision 118).
//
// A provisioner and the usage source that meters what it provisions, a
// notifier and the login provider for the same chat platform — integrations
// come in sets, and an operator installing "the Keyva plugin" should get all
// of it from one download. ServeBundle serves every implementation in a
// Bundle from one process: each kind under its own dispense key, exactly as
// the single-kind Serve* entry points do, plus the Bundle manifest service
// under BundlePluginName so the host knows which keys to dispense. The
// single-kind entry points are one-field bundles, so every SDK-built binary
// answers the manifest and there is one serving path.

// BundlePluginName is the dispense key the manifest is served under — a
// cross-kind facility like WidgetsPluginName, not a kind.
const BundlePluginName = "bundle"

// Kinds is the closed set of plugin kinds in the design of record's §3
// order: the dispense keys, which are also the kind names and the
// binary-name prefixes (`<kind>-<name>`). The first kind a bundle serves in
// this order is its primary when its binary name says nothing.
var Kinds = []string{
	GatewayPluginName, ProvisionerPluginName, UsageSourcePluginName, NotifierPluginName,
	RegistrarPluginName, FraudPluginName, DNSPluginName, BackupPluginName,
	AccountingPluginName, LoginPluginName, OOBPluginName, IPAMPluginName,
}

// KnownKind reports whether kind is one of Kinds.
func KnownKind(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Bundle is the set of kind implementations one binary serves together.
// Leave the kinds it does not serve nil; at least one must be set.
type Bundle struct {
	// Storage is shared by every kind in this process. When nil, the primary
	// kind's implementation is checked for StorageProvider.
	Storage        StorageProvider
	Gateway        PaymentGateway
	Provisioner    Provisioner
	UsageSource    UsageSource
	Notifier       Notifier
	Registrar      Registrar
	FraudScreen    FraudScreen
	DNSProvider    DNSProvider
	BackupProvider BackupProvider
	Accounting     AccountingProvider
	LoginProvider  LoginProvider
	OOBProvider    OOBProvider
	IPAMProvider   IPAMProvider
}

// ServedKind is one kind a binary serves, as -rb-plugin-info reports it: the
// kind and the identity that kind's Describe declares. Maturity is the
// declared value; consumers normalize it exactly as the host does. Version
// is the declared Describe version, which first-party plugins leave empty
// (the release lock assigns theirs, §17.4) and the publisher refuses.
type ServedKind struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Version     string `json:"version,omitempty"`
	Maturity    string `json:"maturity"`
}

// served is one kind a bundle serves: its dispense key, the go-plugin
// adapter, the implementation (for the widget check), and its Describe
// reduced to the identity the info flag prints.
type served struct {
	kind     string
	plugin   goplugin.Plugin
	impl     any
	describe func(context.Context) (ServedKind, error)
}

// served lists the kinds the bundle serves, in Kinds order.
func (b Bundle) served() []served {
	var out []served
	add := func(kind string, impl any, plugin goplugin.Plugin, describe func(context.Context) (ServedKind, error)) {
		out = append(out, served{kind: kind, plugin: plugin, impl: impl, describe: describe})
	}
	identity := func(kind, name, display, version, maturity string) ServedKind {
		return ServedKind{Kind: kind, Name: name, DisplayName: display, Version: version, Maturity: maturity}
	}
	if impl := b.Gateway; impl != nil {
		add(GatewayPluginName, impl, &GatewayPlugin{Impl: impl}, func(ctx context.Context) (ServedKind, error) {
			i, err := impl.Describe(ctx)
			return identity(GatewayPluginName, i.Name, i.DisplayName, i.Version, i.Maturity), err
		})
	}
	if impl := b.Provisioner; impl != nil {
		add(ProvisionerPluginName, impl, &ProvisionerPlugin{Impl: impl}, func(ctx context.Context) (ServedKind, error) {
			i, err := impl.Describe(ctx)
			return identity(ProvisionerPluginName, i.Name, i.DisplayName, i.Version, i.Maturity), err
		})
	}
	if impl := b.UsageSource; impl != nil {
		add(UsageSourcePluginName, impl, &UsageSourcePlugin{Impl: impl}, func(ctx context.Context) (ServedKind, error) {
			i, err := impl.Describe(ctx)
			return identity(UsageSourcePluginName, i.Name, i.DisplayName, i.Version, i.Maturity), err
		})
	}
	if impl := b.Notifier; impl != nil {
		add(NotifierPluginName, impl, &NotifierPlugin{Impl: impl}, func(ctx context.Context) (ServedKind, error) {
			i, err := impl.Describe(ctx)
			return identity(NotifierPluginName, i.Name, i.DisplayName, i.Version, i.Maturity), err
		})
	}
	if impl := b.Registrar; impl != nil {
		add(RegistrarPluginName, impl, &RegistrarPlugin{Impl: impl}, func(ctx context.Context) (ServedKind, error) {
			i, err := impl.Describe(ctx)
			return identity(RegistrarPluginName, i.Name, i.DisplayName, i.Version, i.Maturity), err
		})
	}
	if impl := b.FraudScreen; impl != nil {
		add(FraudPluginName, impl, &FraudPlugin{Impl: impl}, func(ctx context.Context) (ServedKind, error) {
			i, err := impl.Describe(ctx)
			return identity(FraudPluginName, i.Name, i.DisplayName, i.Version, i.Maturity), err
		})
	}
	if impl := b.DNSProvider; impl != nil {
		add(DNSPluginName, impl, &DNSPlugin{Impl: impl}, func(ctx context.Context) (ServedKind, error) {
			i, err := impl.Describe(ctx)
			return identity(DNSPluginName, i.Name, i.DisplayName, i.Version, i.Maturity), err
		})
	}
	if impl := b.BackupProvider; impl != nil {
		add(BackupPluginName, impl, &BackupPlugin{Impl: impl}, func(ctx context.Context) (ServedKind, error) {
			i, err := impl.Describe(ctx)
			return identity(BackupPluginName, i.Name, i.DisplayName, i.Version, i.Maturity), err
		})
	}
	if impl := b.Accounting; impl != nil {
		add(AccountingPluginName, impl, &AccountingPlugin{Impl: impl}, func(ctx context.Context) (ServedKind, error) {
			i, err := impl.Describe(ctx)
			return identity(AccountingPluginName, i.Name, i.DisplayName, i.Version, i.Maturity), err
		})
	}
	if impl := b.LoginProvider; impl != nil {
		add(LoginPluginName, impl, &LoginPlugin{Impl: impl}, func(ctx context.Context) (ServedKind, error) {
			i, err := impl.Describe(ctx)
			return identity(LoginPluginName, i.Name, i.DisplayName, i.Version, i.Maturity), err
		})
	}
	if impl := b.OOBProvider; impl != nil {
		add(OOBPluginName, impl, &OOBPlugin{Impl: impl}, func(ctx context.Context) (ServedKind, error) {
			i, err := impl.Describe(ctx)
			return identity(OOBPluginName, i.Name, i.DisplayName, i.Version, i.Maturity), err
		})
	}
	if impl := b.IPAMProvider; impl != nil {
		add(IPAMPluginName, impl, &IPAMPlugin{Impl: impl}, func(ctx context.Context) (ServedKind, error) {
			i, err := impl.Describe(ctx)
			return identity(IPAMPluginName, i.Name, i.DisplayName, i.Version, i.Maturity), err
		})
	}
	return out
}

// primaryIndex picks the bundle's primary kind: the one the binary is named
// for, which is the kind the host's discovery routes on and describes first
// (§3: the `<kind>-` prefix). A binary named for none of its kinds — a test
// build, a renamed file — reports the first kind it serves in Kinds order.
func primaryIndex(kinds []served, binaryName string) int {
	for i, k := range kinds {
		if strings.HasPrefix(binaryName, k.kind+"-") {
			return i
		}
	}
	return 0
}

// ServeBundle serves every implementation in b from this process. The
// single-kind Serve* entry points are one-field bundles, so this is the one
// serving path: -rb-plugin-info is answered first (InfoFlag), then the
// go-plugin handshake with one dispense key per kind, the Bundle manifest,
// and — when any implementation offers dashboard widgets (§14b) — the
// widgets service, serving the union of what they offer.
//
// A bundle that serves nothing is a programming error and exits before the
// handshake, so a misassembled binary fails its load with the reason in the
// plugin log rather than answering nothing.
func ServeBundle(b Bundle) {
	kinds := b.served()
	if len(kinds) == 0 {
		fmt.Fprintln(os.Stderr, "rackbill plugin: ServeBundle called with no implementations — set at least one Bundle field")
		os.Exit(1)
	}
	primary := primaryIndex(kinds, filepath.Base(os.Args[0]))
	serveInfo(kinds, primary)
	serveSetup()

	plugins := make(map[string]goplugin.Plugin, len(kinds)+2)
	names := make([]string, 0, len(kinds))
	var widgets []WidgetProvider
	for _, k := range kinds {
		plugins[k.kind] = k.plugin
		names = append(names, k.kind)
		if wp, ok := k.impl.(WidgetProvider); ok {
			widgets = append(widgets, wp)
		}
	}
	switch len(widgets) {
	case 0:
	case 1:
		plugins[WidgetsPluginName] = &WidgetPlugin{Impl: widgets[0]}
	default:
		plugins[WidgetsPluginName] = &WidgetPlugin{Impl: mergedWidgets(widgets)}
	}
	plugins[BundlePluginName] = &BundlePlugin{Kinds: names}
	storage := b.Storage
	if storage == nil {
		storage, _ = kinds[primary].impl.(StorageProvider)
	}
	if storage != nil {
		plugins[StoragePluginName] = &StoragePlugin{Impl: storage}
	}
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         plugins,
		GRPCServer:      grpcServer,
	})
}

// mergedWidgets is the widget offering of a bundle whose implementations
// each offer some: DescribeWidgets is the union, and GetWidgetData routes by
// widget id to whichever implementation declared it. Ids must be unique
// across the binary — a collision is reported by the probe, which the host
// answers by disabling the binary's widgets and saying why, never by
// guessing which implementation meant it.
type mergedWidgets []WidgetProvider

func (m mergedWidgets) DescribeWidgets(ctx context.Context) ([]WidgetDescriptor, error) {
	var out []WidgetDescriptor
	seen := map[string]bool{}
	for _, wp := range m {
		ws, err := wp.DescribeWidgets(ctx)
		if err != nil {
			return nil, err
		}
		for _, w := range ws {
			if seen[w.ID] {
				return nil, fmt.Errorf("widget id %q is declared by more than one implementation in this bundle", w.ID)
			}
			seen[w.ID] = true
			out = append(out, w)
		}
	}
	return out, nil
}

func (m mergedWidgets) GetWidgetData(ctx context.Context, in WidgetDataInput) (WidgetDataResult, error) {
	for _, wp := range m {
		ws, err := wp.DescribeWidgets(ctx)
		if err != nil {
			return WidgetDataResult{}, err
		}
		for _, w := range ws {
			if w.ID == in.WidgetID {
				return wp.GetWidgetData(ctx, in)
			}
		}
	}
	return WidgetDataResult{}, fmt.Errorf("unknown widget %q", in.WidgetID)
}

// BundleDescriber is the host's view of the manifest service.
type BundleDescriber interface {
	// DescribeBundle lists the dispense keys the process serves.
	DescribeBundle(ctx context.Context) ([]string, error)
}

// BundlePlugin adapts the manifest across the go-plugin gRPC boundary. The
// client side is a stub the host always dispenses; whether the server side
// exists is what the probe finds out.
type BundlePlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Kinds []string
}

func (p *BundlePlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterBundleServer(s, &bundleGRPCServer{kinds: p.Kinds})
	return nil
}

func (p *BundlePlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &bundleGRPCClient{client: proto.NewBundleClient(c)}, nil
}

type bundleGRPCServer struct {
	proto.UnimplementedBundleServer
	kinds []string
}

func (s *bundleGRPCServer) DescribeBundle(context.Context, *proto.DescribeBundleRequest) (*proto.DescribeBundleResponse, error) {
	return &proto.DescribeBundleResponse{Kinds: s.kinds}, nil
}

type bundleGRPCClient struct {
	client proto.BundleClient
}

func (c *bundleGRPCClient) DescribeBundle(ctx context.Context) ([]string, error) {
	resp, err := c.client.DescribeBundle(ctx, &proto.DescribeBundleRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Kinds, nil
}
