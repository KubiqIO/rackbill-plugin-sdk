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
	"rackbill.com/sdk/plugin/proto"
)

// zoneRegistrar implements the contract 12 zone calls and records what arrived.
type zoneRegistrar struct {
	Registrar
	put []ZoneRecord
}

func (z *zoneRegistrar) Describe(context.Context) (RegistrarInfo, error) {
	return RegistrarInfo{Name: "t", SupportsDNSHosting: true, MaxRegistrationYears: 10}, nil
}
func (z *zoneRegistrar) ListZoneRecords(_ context.Context, ref DomainRef) ([]ZoneRecord, bool, error) {
	if ref.Domain != "shop.example" {
		return nil, false, nil
	}
	return []ZoneRecord{{Name: "", Type: RecordMX, Value: "mail.shop.example", TTLSeconds: 3600, Priority: 10}}, true, nil
}
func (z *zoneRegistrar) PutZone(_ context.Context, _ DomainRef, records []ZoneRecord) (ZoneOpResult, error) {
	z.put = records
	return ZoneOpResult{Status: DomainOpSucceeded, Nameservers: []string{"ns1.example", "ns2.example"}}, nil
}
func (z *zoneRegistrar) DeleteZone(context.Context, DomainRef) (ZoneOpResult, error) {
	return ZoneOpResult{Status: DomainOpFailed, FailureReason: "zone locked"}, nil
}

func dialRegistrar(t *testing.T, impl Registrar) Registrar {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	proto.RegisterRegistrarServer(srv, &registrarGRPCServer{impl: impl})
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///buf", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &registrarGRPCClient{client: proto.NewRegistrarClient(conn)}
}

func TestZoneHostingRoundTrip(t *testing.T) {
	impl := &zoneRegistrar{}
	c := dialRegistrar(t, impl)
	z := c.(ZoneHoster)
	ctx := t.Context()

	if info, err := c.Describe(ctx); err != nil || !info.SupportsDNSHosting || info.MaxRegistrationYears != 10 {
		t.Fatalf("describe lost supports_dns_hosting: %+v %v", info, err)
	}
	recs, found, err := z.ListZoneRecords(ctx, DomainRef{Domain: "shop.example"})
	want := []ZoneRecord{{Name: "", Type: RecordMX, Value: "mail.shop.example", TTLSeconds: 3600, Priority: 10}}
	if err != nil || !found || !reflect.DeepEqual(recs, want) {
		t.Fatalf("list = %+v %v %v", recs, found, err)
	}
	if _, found, _ := z.ListZoneRecords(ctx, DomainRef{Domain: "none.example"}); found {
		t.Fatal("absent zone reported found")
	}
	put := []ZoneRecord{{Name: "www", Type: RecordA, Value: "192.0.2.1", TTLSeconds: 300}, {Name: "_sip._tcp", Type: RecordSRV, Value: "5 5060 sip.example", Priority: 10}}
	res, err := z.PutZone(ctx, DomainRef{Domain: "shop.example"}, put)
	if err != nil || res.Status != DomainOpSucceeded || len(res.Nameservers) != 2 || !reflect.DeepEqual(impl.put, put) {
		t.Fatalf("put = %+v %v, arrived %+v", res, err, impl.put)
	}
	if res, err := z.DeleteZone(ctx, DomainRef{Domain: "shop.example"}); err != nil || res.Status != DomainOpFailed || res.FailureReason != "zone locked" {
		t.Fatalf("delete = %+v %v", res, err)
	}
}

// A registrar without the capability answers UNIMPLEMENTED, not a crash.
func TestZoneHostingUnimplemented(t *testing.T) {
	c := dialRegistrar(t, struct{ Registrar }{})
	_, _, err := c.(ZoneHoster).ListZoneRecords(t.Context(), DomainRef{Domain: "shop.example"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("err = %v, want Unimplemented", err)
	}
}
