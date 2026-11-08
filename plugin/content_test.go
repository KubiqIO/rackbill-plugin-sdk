package plugin

import (
	"context"
	"net"
	"reflect"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type testContent struct {
	info ContentInfo
	err  error
}

func (p *testContent) Describe(context.Context) (ContentInfo, error) { return p.info, p.err }

func TestContentProviderTransport(t *testing.T) {
	impl := &testContent{info: ContentInfo{
		Name: "german", DisplayName: "German translations", Version: "1.2.3", Maturity: MaturityBeta,
		Packs: []ContentPack{{ID: "de", DisplayName: "Deutsch", Locale: "de", Values: map[string]string{
			"nav.services": "Dienste", "count": "{count, plural, one {# Dienst} other {# Dienste}}",
		}}, {ID: "english", DisplayName: "English", Locale: "en", Values: map[string]string{"nav.services": "Services"}}},
	}}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	if err := (&ContentPlugin{Impl: impl}).GRPCServer(nil, srv); err != nil {
		t.Fatal(err)
	}
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///buf", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	raw, err := (&ContentPlugin{}).GRPCClient(t.Context(), nil, conn)
	if err != nil {
		t.Fatal(err)
	}
	client := raw.(ContentProvider)
	got, err := client.Describe(t.Context())
	if err != nil || !reflect.DeepEqual(got, impl.info) {
		t.Fatalf("round trip: %+v, %v", got, err)
	}
	impl.err = status.Error(codes.InvalidArgument, "invalid source file")
	if _, err := client.Describe(t.Context()); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("provider error was lost: %v", err)
	}
}

func TestContentProviderBundleIdentity(t *testing.T) {
	provider := &testContent{info: ContentInfo{Name: "german", DisplayName: "Deutsch", Version: "1.0.0", Maturity: MaturityStable}}
	for _, bundle := range []Bundle{{ContentProvider: provider}, {UsageSource: testUsage{"usage"}, ContentProvider: provider}} {
		kinds := bundle.served()
		primary := primaryIndex(kinds, "content-german")
		got, err := kinds[primary].describe(t.Context())
		want := ServedKind{Kind: ContentPluginName, Name: "german", DisplayName: "Deutsch", Version: "1.0.0", Maturity: MaturityStable}
		if err != nil || got != want || !KnownKind(got.Kind) {
			t.Fatalf("content identity: %+v, %v", got, err)
		}
	}
}
