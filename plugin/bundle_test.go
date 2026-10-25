package plugin

import (
	"context"
	"strings"
	"testing"
)

type testUsage struct{ name string }

func (u testUsage) Describe(context.Context) (UsageSourceInfo, error) {
	return UsageSourceInfo{Name: u.name, DisplayName: "Usage " + u.name, Maturity: MaturityBeta}, nil
}

func (testUsage) Collect(context.Context, CollectInput) ([]UsageRecord, error) { return nil, nil }

// testProvisioner embeds the interface it only partly implements: the bundle
// calls nothing but Describe here.
type testProvisioner struct{ Provisioner }

func (testProvisioner) Describe(context.Context) (ProvisionerInfo, error) {
	return ProvisionerInfo{Name: "prov", DisplayName: "Prov", Version: "9.9.9"}, nil
}

// TestBundleServesInKindOrder pins what -rb-plugin-info and the manifest
// report: the kinds a bundle serves come out in the §3 table's order
// whatever order the fields were set in, the binary's name picks the
// primary, and a name for none of them means the first.
func TestBundleServesInKindOrder(t *testing.T) {
	b := Bundle{UsageSource: testUsage{"u"}, Provisioner: testProvisioner{}}
	kinds := b.served()
	var names []string
	for _, k := range kinds {
		names = append(names, k.kind)
	}
	if got := strings.Join(names, ","); got != "provisioner,usage" {
		t.Fatalf("served order %q, want provisioner,usage", got)
	}
	for name, want := range map[string]int{
		"provisioner-keyva": 0, "usage-keyva": 1, "keyva": 0, "gateway-x": 0, "usage-keyva.exe": 1,
	} {
		if got := primaryIndex(kinds, name); got != want {
			t.Errorf("primaryIndex(%q) = %d, want %d", name, got, want)
		}
	}
	ctx := context.Background()
	sk, err := kinds[1].describe(ctx)
	if err != nil || sk != (ServedKind{Kind: "usage", Name: "u", DisplayName: "Usage u", Maturity: MaturityBeta}) {
		t.Fatalf("usage identity %+v %v", sk, err)
	}
	sk, err = kinds[0].describe(ctx)
	if err != nil || sk != (ServedKind{Kind: "provisioner", Name: "prov", DisplayName: "Prov", Version: "9.9.9"}) {
		t.Fatalf("provisioner identity %+v %v", sk, err)
	}
	if len((Bundle{}).served()) != 0 {
		t.Fatal("an empty bundle serves nothing")
	}
}

// TestBinaryInfoServedKinds folds the pre-bundle info shape: a binary that
// printed no serves list is exactly its one kind.
func TestBinaryInfoServedKinds(t *testing.T) {
	old := BinaryInfo{Schema: 1, Kind: "gateway", Name: "stripe", DisplayName: "Stripe", Maturity: "beta"}
	got := old.ServedKinds()
	if len(got) != 1 || got[0] != (ServedKind{Kind: "gateway", Name: "stripe", DisplayName: "Stripe", Maturity: "beta"}) {
		t.Fatalf("older shape folded to %+v", got)
	}
	bundle := BinaryInfo{Kind: "provisioner", Name: "keyva", Serves: []ServedKind{
		{Kind: "provisioner", Name: "keyva"}, {Kind: "usage", Name: "keyva-usage"},
	}}
	if got := bundle.ServedKinds(); len(got) != 2 || got[1].Name != "keyva-usage" {
		t.Fatalf("bundle shape %+v", got)
	}
}

type testWidgets struct {
	ids  []string
	hits map[string]int
}

func (w testWidgets) DescribeWidgets(context.Context) ([]WidgetDescriptor, error) {
	out := make([]WidgetDescriptor, 0, len(w.ids))
	for _, id := range w.ids {
		out = append(out, WidgetDescriptor{ID: id, Title: id, Shape: WidgetShapeStat})
	}
	return out, nil
}

func (w testWidgets) GetWidgetData(_ context.Context, in WidgetDataInput) (WidgetDataResult, error) {
	w.hits[in.WidgetID]++
	return WidgetDataResult{Status: WidgetOK}, nil
}

// TestMergedWidgets: a bundle whose implementations each offer widgets
// serves the union and routes each id to the implementation that declared
// it; a duplicate id is an error the probe reports rather than a guess.
func TestMergedWidgets(t *testing.T) {
	ctx := context.Background()
	a := testWidgets{ids: []string{"balance"}, hits: map[string]int{}}
	b := testWidgets{ids: []string{"seats", "keys"}, hits: map[string]int{}}
	m := mergedWidgets{a, b}
	ws, err := m.DescribeWidgets(ctx)
	if err != nil || len(ws) != 3 || ws[0].ID != "balance" || ws[2].ID != "keys" {
		t.Fatalf("union %+v %v", ws, err)
	}
	if res, err := m.GetWidgetData(ctx, WidgetDataInput{WidgetID: "seats"}); err != nil || res.Status != WidgetOK {
		t.Fatalf("route: %+v %v", res, err)
	}
	if b.hits["seats"] != 1 || len(a.hits) != 0 {
		t.Fatalf("routed to the wrong implementation: a=%v b=%v", a.hits, b.hits)
	}
	if _, err := m.GetWidgetData(ctx, WidgetDataInput{WidgetID: "nope"}); err == nil {
		t.Fatal("unknown widget id must error")
	}
	dup := mergedWidgets{a, a}
	if _, err := dup.DescribeWidgets(ctx); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("duplicate id: %v", err)
	}
}

func TestKnownKind(t *testing.T) {
	for _, k := range Kinds {
		if !KnownKind(k) {
			t.Errorf("%q not known", k)
		}
	}
	for _, k := range []string{"", WidgetsPluginName, BundlePluginName, "Gateway"} {
		if KnownKind(k) {
			t.Errorf("%q should not be a kind", k)
		}
	}
}
