package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"kubiq.io/rackbill/sdk/plugin/proto"
)

// WidgetsPluginName is the dispense key any plugin binary may additionally
// serve dashboard widgets under (§14b) — a cross-kind facility, not a kind.
// The Serve* entry points wire it automatically when the implementation also
// implements WidgetProvider; the host probes DescribeWidgets at load and
// treats UNIMPLEMENTED as "no widgets".
const WidgetsPluginName = "widgets"

// Widget shapes: which one WidgetData payload field the widget fills.
const (
	WidgetShapeStat   = "stat"
	WidgetShapeSeries = "series"
	WidgetShapeGauge  = "gauge"
	WidgetShapeList   = "list"
	WidgetShapeStatus = "status"
	WidgetShapeTable  = "table"
)

// Widget scopes: how the host resolves the config delivered to GetWidgetData.
const (
	WidgetScopeTenant = "tenant" // the plugin's tenant-level config (§15)
	WidgetScopeNode   = "node"   // one call per registered node, node config
)

// Widget data statuses.
const (
	WidgetOK     = "ok"
	WidgetFailed = "failed"
)

// Status-item states rendered as LEDs by the panel.
const (
	WidgetStatusOK      = "ok"
	WidgetStatusWarn    = "warn"
	WidgetStatusError   = "error"
	WidgetStatusUnknown = "unknown"
)

// WidgetDescriptor describes one widget a plugin offers. JSON tags double as
// the admin API encoding — the panel renders the catalog from these.
type WidgetDescriptor struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Shape       string `json:"shape"`
	Scope       string `json:"scope,omitempty"` // "" = tenant
	Size        string `json:"size,omitempty"`  // "sm" | "md" | "lg" render hint
	// RefreshSeconds is the suggested cache TTL; 0 = host default.
	RefreshSeconds int `json:"refresh_seconds,omitempty"`
}

// WidgetStatValue is one labelled figure. Money is integer minor units + ISO
// currency (AmountCents is only read when Currency is non-empty); quantities
// are doubles with a unit hint ("count", "bytes", "percent", free suffix).
type WidgetStatValue struct {
	Label       string  `json:"label"`
	Value       float64 `json:"value,omitempty"`
	AmountCents int64   `json:"amount_cents,omitempty"`
	Currency    string  `json:"currency,omitempty"`
	Unit        string  `json:"unit,omitempty"`
	DeltaPct    float64 `json:"delta_pct,omitempty"`
	// Href is an optional panel-relative path ("/admin/tickets?status=open")
	// the figure links to — the list or queue it counts. The panel renders
	// only root-relative paths as links and ignores anything else, so a
	// widget can steer within the panel but never off it.
	Href string `json:"href,omitempty"`
}

type WidgetSeriesPoint struct {
	TS    int64   `json:"ts"` // unix seconds
	Value float64 `json:"value"`
}

type WidgetSeriesLine struct {
	Label  string              `json:"label"`
	Points []WidgetSeriesPoint `json:"points"`
}

type WidgetStat struct {
	// Values[0] renders as the hero figure, the rest as secondary rows.
	Values []WidgetStatValue   `json:"values"`
	Spark  []WidgetSeriesPoint `json:"spark,omitempty"`
}

type WidgetSeries struct {
	Lines []WidgetSeriesLine `json:"lines"`
	// Unit: "count", "bytes", or "money:<ISO>" (values then in minor units).
	Unit string `json:"unit,omitempty"`
	Kind string `json:"kind,omitempty"` // render hint: "line" | "bar" | "area"
}

type WidgetGauge struct {
	Label  string  `json:"label"`
	Used   float64 `json:"used"`
	Limit  float64 `json:"limit,omitempty"` // 0 = unlimited/unknown
	Unit   string  `json:"unit,omitempty"`
	Detail string  `json:"detail,omitempty"`
}

type WidgetListItem struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Status   string `json:"status,omitempty"`
	TS       int64  `json:"ts,omitempty"` // unix seconds
}

type WidgetStatusItem struct {
	Label  string `json:"label"`
	Status string `json:"status"` // ok | warn | error | unknown
	Detail string `json:"detail,omitempty"`
}

type WidgetTable struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// WidgetData carries exactly one shape, matching the descriptor.
type WidgetData struct {
	Stat     *WidgetStat        `json:"stat,omitempty"`
	Series   *WidgetSeries      `json:"series,omitempty"`
	Gauges   []WidgetGauge      `json:"gauges,omitempty"`
	Items    []WidgetListItem   `json:"items,omitempty"`
	Statuses []WidgetStatusItem `json:"statuses,omitempty"`
	Table    *WidgetTable       `json:"table,omitempty"`
}

type WidgetDataInput struct {
	TenantID string
	WidgetID string
	// Config is resolved by the host per the descriptor's scope; empty means
	// fall back to the process environment, as everywhere.
	Config map[string]string
}

type WidgetDataResult struct {
	Status        string // "ok" or "failed"
	FailureReason string
	Data          WidgetData
}

// WidgetProvider is implemented by plugins that offer dashboard widgets, in
// addition to their kind interface. The same statelessness and concurrency
// rules apply (§7.3).
type WidgetProvider interface {
	DescribeWidgets(ctx context.Context) ([]WidgetDescriptor, error)
	GetWidgetData(ctx context.Context, in WidgetDataInput) (WidgetDataResult, error)
}

// WidgetPlugin adapts a WidgetProvider across the go-plugin gRPC boundary.
type WidgetPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl WidgetProvider
}

func (p *WidgetPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterWidgetProviderServer(s, &widgetGRPCServer{impl: p.Impl})
	return nil
}

func (p *WidgetPlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &widgetGRPCClient{client: proto.NewWidgetProviderClient(c)}, nil
}

// servePlugins builds the map for a Serve* entry point, adding the widgets
// service when the kind implementation also implements WidgetProvider.
// --- gRPC adapters ---

type widgetGRPCServer struct {
	proto.UnimplementedWidgetProviderServer
	impl WidgetProvider
}

func (s *widgetGRPCServer) DescribeWidgets(ctx context.Context, _ *proto.DescribeWidgetsRequest) (*proto.DescribeWidgetsResponse, error) {
	ws, err := s.impl.DescribeWidgets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*proto.WidgetDescriptor, len(ws))
	for i, w := range ws {
		out[i] = &proto.WidgetDescriptor{
			Id: w.ID, Title: w.Title, Description: w.Description,
			Shape: w.Shape, Scope: w.Scope, Size: w.Size,
			RefreshSeconds: int32(w.RefreshSeconds),
		}
	}
	return &proto.DescribeWidgetsResponse{Widgets: out}, nil
}

func (s *widgetGRPCServer) GetWidgetData(ctx context.Context, req *proto.WidgetDataRequest) (*proto.WidgetDataResponse, error) {
	res, err := s.impl.GetWidgetData(ctx, WidgetDataInput{
		TenantID: req.TenantId, WidgetID: req.WidgetId, Config: req.Config,
	})
	if err != nil {
		return nil, err
	}
	return &proto.WidgetDataResponse{
		Status: res.Status, FailureReason: res.FailureReason,
		Data: widgetDataToProto(res.Data),
	}, nil
}

type widgetGRPCClient struct {
	client proto.WidgetProviderClient
}

func (c *widgetGRPCClient) DescribeWidgets(ctx context.Context) ([]WidgetDescriptor, error) {
	resp, err := c.client.DescribeWidgets(ctx, &proto.DescribeWidgetsRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]WidgetDescriptor, len(resp.Widgets))
	for i, w := range resp.Widgets {
		out[i] = WidgetDescriptor{
			ID: w.Id, Title: w.Title, Description: w.Description,
			Shape: w.Shape, Scope: w.Scope, Size: w.Size,
			RefreshSeconds: int(w.RefreshSeconds),
		}
	}
	return out, nil
}

func (c *widgetGRPCClient) GetWidgetData(ctx context.Context, in WidgetDataInput) (WidgetDataResult, error) {
	resp, err := c.client.GetWidgetData(ctx, &proto.WidgetDataRequest{
		TenantId: in.TenantID, WidgetId: in.WidgetID, Config: in.Config,
	})
	if err != nil {
		return WidgetDataResult{}, err
	}
	return WidgetDataResult{
		Status: resp.Status, FailureReason: resp.FailureReason,
		Data: widgetDataFromProto(resp.Data),
	}, nil
}

func widgetDataToProto(d WidgetData) *proto.WidgetData {
	out := &proto.WidgetData{}
	if d.Stat != nil {
		st := &proto.WidgetStat{Spark: pointsToProto(d.Stat.Spark)}
		for _, v := range d.Stat.Values {
			st.Values = append(st.Values, &proto.WidgetStatValue{
				Label: v.Label, Value: v.Value, AmountCents: v.AmountCents,
				Currency: v.Currency, Unit: v.Unit, DeltaPct: v.DeltaPct, Href: v.Href,
			})
		}
		out.Stat = st
	}
	if d.Series != nil {
		se := &proto.WidgetSeries{Unit: d.Series.Unit, Kind: d.Series.Kind}
		for _, l := range d.Series.Lines {
			se.Lines = append(se.Lines, &proto.WidgetSeriesLine{Label: l.Label, Points: pointsToProto(l.Points)})
		}
		out.Series = se
	}
	for _, g := range d.Gauges {
		out.Gauges = append(out.Gauges, &proto.WidgetGauge{
			Label: g.Label, Used: g.Used, Limit: g.Limit, Unit: g.Unit, Detail: g.Detail,
		})
	}
	for _, it := range d.Items {
		out.Items = append(out.Items, &proto.WidgetListItem{
			Title: it.Title, Subtitle: it.Subtitle, Status: it.Status, Ts: it.TS,
		})
	}
	for _, st := range d.Statuses {
		out.Statuses = append(out.Statuses, &proto.WidgetStatusItem{
			Label: st.Label, Status: st.Status, Detail: st.Detail,
		})
	}
	if d.Table != nil {
		tb := &proto.WidgetTable{Columns: d.Table.Columns}
		for _, r := range d.Table.Rows {
			tb.Rows = append(tb.Rows, &proto.WidgetTableRow{Cells: r})
		}
		out.Table = tb
	}
	return out
}

func widgetDataFromProto(d *proto.WidgetData) WidgetData {
	var out WidgetData
	if d == nil {
		return out
	}
	if d.Stat != nil {
		st := &WidgetStat{Spark: pointsFromProto(d.Stat.Spark)}
		for _, v := range d.Stat.Values {
			st.Values = append(st.Values, WidgetStatValue{
				Label: v.Label, Value: v.Value, AmountCents: v.AmountCents,
				Currency: v.Currency, Unit: v.Unit, DeltaPct: v.DeltaPct, Href: v.Href,
			})
		}
		out.Stat = st
	}
	if d.Series != nil {
		se := &WidgetSeries{Unit: d.Series.Unit, Kind: d.Series.Kind}
		for _, l := range d.Series.Lines {
			se.Lines = append(se.Lines, WidgetSeriesLine{Label: l.Label, Points: pointsFromProto(l.Points)})
		}
		out.Series = se
	}
	for _, g := range d.Gauges {
		out.Gauges = append(out.Gauges, WidgetGauge{
			Label: g.Label, Used: g.Used, Limit: g.Limit, Unit: g.Unit, Detail: g.Detail,
		})
	}
	for _, it := range d.Items {
		out.Items = append(out.Items, WidgetListItem{
			Title: it.Title, Subtitle: it.Subtitle, Status: it.Status, TS: it.Ts,
		})
	}
	for _, st := range d.Statuses {
		out.Statuses = append(out.Statuses, WidgetStatusItem{
			Label: st.Label, Status: st.Status, Detail: st.Detail,
		})
	}
	if d.Table != nil {
		tb := &WidgetTable{Columns: d.Table.Columns}
		for _, r := range d.Table.Rows {
			tb.Rows = append(tb.Rows, r.Cells)
		}
		out.Table = tb
	}
	return out
}

func pointsToProto(ps []WidgetSeriesPoint) []*proto.WidgetSeriesPoint {
	out := make([]*proto.WidgetSeriesPoint, len(ps))
	for i, p := range ps {
		out[i] = &proto.WidgetSeriesPoint{Ts: p.TS, Value: p.Value}
	}
	return out
}

func pointsFromProto(ps []*proto.WidgetSeriesPoint) []WidgetSeriesPoint {
	if len(ps) == 0 {
		return nil
	}
	out := make([]WidgetSeriesPoint, len(ps))
	for i, p := range ps {
		out[i] = WidgetSeriesPoint{TS: p.Ts, Value: p.Value}
	}
	return out
}
