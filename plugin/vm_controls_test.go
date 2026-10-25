package plugin

import (
	"context"
	"net"
	"slices"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"rackbill.com/sdk/plugin/proto"
)

// controlsVM implements the contract 9 VM calls and records what arrived.
type controlsVM struct {
	Provisioner
	hostname, rdnsAddr, rdnsDomain, groupID, newName string
	reinstallKeys, provisionKeys, accountKeys        []SSHKey
}

func (v *controlsVM) Describe(context.Context) (ProvisionerInfo, error) {
	return ProvisionerInfo{Name: "t", SupportsVM: true, SupportsHostname: true, SupportsNetwork: true,
		SupportsReverseDNS: true, SupportsGroups: true, SupportsSSHKeys: true, SupportsReinstall: true,
		ReinstallKey: "os", ReinstallAppKey: "app"}, nil
}
func (v *controlsVM) Provision(_ context.Context, in ProvisionInput) (ProvisionResult, error) {
	v.provisionKeys, v.accountKeys = in.SSHKeys, in.AccountSSHKeys
	return ProvisionResult{Status: ProvisionSucceeded}, nil
}
func (v *controlsVM) VMReinstall(_ context.Context, ref VMRef) (string, error) {
	v.reinstallKeys = ref.SSHKeys
	return "", nil
}
func (v *controlsVM) VMSetHostname(_ context.Context, _ VMRef, h string) error {
	if h == "taken.example" {
		return Refused("hostname %s is taken", h)
	}
	v.hostname = h
	return nil
}
func (v *controlsVM) VMAddresses(context.Context, VMRef) ([]VMAddress, error) {
	return []VMAddress{
		{Address: "192.0.2.10", Primary: true, ReverseDNS: "host.example", ReverseDNSEditable: true},
		{Address: "2001:db8::1", Range: "2001:db8::/64"},
	}, nil
}
func (v *controlsVM) VMSetReverseDNS(_ context.Context, _ VMRef, addr, domain string) error {
	v.rdnsAddr, v.rdnsDomain = addr, domain
	return nil
}
func (v *controlsVM) VMGroups(context.Context, VMRef) (VMGroups, error) {
	return VMGroups{CurrentID: "1", CanCreate: true, Groups: []VMGroup{{ID: "1", Name: "Default"}, {ID: "2", Name: "Web"}}}, nil
}
func (v *controlsVM) VMSetGroup(_ context.Context, _ VMRef, id, name string) (VMGroup, error) {
	v.groupID, v.newName = id, name
	return VMGroup{ID: "3", Name: name}, nil
}

func TestVMControlsRoundTrip(t *testing.T) {
	impl := &controlsVM{}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	proto.RegisterProvisionerServer(srv, ProvisionerServer(impl))
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///buf", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := ProvisionerClient(proto.NewProvisionerClient(conn))
	ctx := t.Context()

	info, err := c.Describe(ctx)
	if err != nil || !info.SupportsHostname || !info.SupportsNetwork || !info.SupportsReverseDNS ||
		!info.SupportsGroups || !info.SupportsSSHKeys || info.ReinstallAppKey != "app" {
		t.Fatalf("describe lost a contract 9 flag: %+v %v", info, err)
	}
	keys := []SSHKey{{Name: "laptop", PublicKey: "ssh-ed25519 AAAA laptop"}}
	all := append([]SSHKey{{Name: "ci", PublicKey: "ssh-ed25519 BBBB ci"}}, keys...)
	if _, err := c.Provision(ctx, ProvisionInput{ServiceID: "s", SSHKeys: keys, AccountSSHKeys: all}); err != nil ||
		!slices.Equal(impl.provisionKeys, keys) || !slices.Equal(impl.accountKeys, all) {
		t.Fatalf("provision keys %+v account %+v err %v", impl.provisionKeys, impl.accountKeys, err)
	}
	ref := VMRef{ServiceID: "s", SSHKeys: keys}
	if _, err := c.(VMReinstaller).VMReinstall(ctx, ref); err != nil || !slices.Equal(impl.reinstallKeys, keys) {
		t.Fatalf("reinstall keys %+v err %v", impl.reinstallKeys, err)
	}
	if err := c.(VMHostnameSetter).VMSetHostname(ctx, ref, "web1.example"); err != nil || impl.hostname != "web1.example" {
		t.Fatalf("hostname %q err %v", impl.hostname, err)
	}
	if reason, ok := IsRefused(c.(VMHostnameSetter).VMSetHostname(ctx, ref, "taken.example")); !ok || reason != "hostname taken.example is taken" {
		t.Fatalf("a refusal must come back as one: %q %v", reason, ok)
	}
	addrs, err := c.(VMNetworkReader).VMAddresses(ctx, ref)
	if err != nil || len(addrs) != 2 || !addrs[0].ReverseDNSEditable || addrs[0].ReverseDNS != "host.example" || addrs[1].Range != "2001:db8::/64" {
		t.Fatalf("addresses %+v err %v", addrs, err)
	}
	if err := c.(VMReverseDNSSetter).VMSetReverseDNS(ctx, ref, "192.0.2.10", "mail.example"); err != nil || impl.rdnsDomain != "mail.example" {
		t.Fatalf("rdns %q %q err %v", impl.rdnsAddr, impl.rdnsDomain, err)
	}
	groups, err := c.(VMGrouper).VMGroups(ctx, ref)
	if err != nil || groups.CurrentID != "1" || !groups.CanCreate || len(groups.Groups) != 2 {
		t.Fatalf("groups %+v err %v", groups, err)
	}
	g, err := c.(VMGrouper).VMSetGroup(ctx, ref, "", "Staging")
	if err != nil || g.ID != "3" || impl.newName != "Staging" {
		t.Fatalf("set group %+v err %v", g, err)
	}
}
