package plugin

import (
	"context"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"kubiq.io/rackbill/sdk/plugin/proto"
)

// OOBPluginName is the key out-of-band providers are served and dispensed
// under.
const OOBPluginName = "oob"

// Power states a BMC reports.
const (
	OOBPowerOn      = "on"
	OOBPowerOff     = "off"
	OOBPowerUnknown = "unknown"
)

// Power actions the agent requests. Already-in-target-state is success (the
// VM-capability rule); a BMC that says no for a reason the user should read
// refuses via sdk.Refused instead.
const (
	OOBActionOn      = "on"
	OOBActionOffSoft = "off_soft" // graceful (ACPI) shutdown request
	OOBActionOffHard = "off_hard" // immediate power removal
	OOBActionCycle   = "cycle"
	OOBActionReset   = "reset" // warm reset without dropping power
)

// Power action statuses.
const (
	OOBSucceeded = "succeeded"
	OOBFailed    = "failed"
)

// Console kinds a caller can ask MintConsole for.
const (
	OOBConsoleSOL = "sol" // text console (Serial-over-LAN or equivalent)
	OOBConsoleKVM = "kvm" // graphical console
)

// Console stream types the mint answers with — what the browser renders.
const (
	OOBConsoleTerm = "term" // raw byte stream for a terminal; stdin up, output down
	OOBConsoleVNC  = "vnc"  // RFB over websocket, rendered by noVNC
)

// Per-device console declarations (OOBDeviceDeclaration, contract 4). Three
// answers, because "no" and "nobody said" must never resolve alike: a declared
// "no" withdraws a console with no probe, a declared "yes" offers it whatever
// a probe found, and the empty string leaves the agent to its probe rule.
const (
	OOBDeclaredYes = "yes"
	OOBDeclaredNo  = "no"
	OOBUndeclared  = ""
)

// Probe statuses. A probe that cannot reach or sign in to the BMC is an
// answer, not an error; capability booleans mean nothing unless "ok".
const (
	OOBProbeOK          = "ok"
	OOBProbeUnreachable = "unreachable"
	OOBProbeAuthFailed  = "auth_failed"
	OOBProbeFailed      = "failed"
)

// Sensor kinds and statuses (ReadOOBSensors).
const (
	OOBSensorTemperature = "temperature"
	OOBSensorFan         = "fan"
	OOBSensorVoltage     = "voltage"
	OOBSensorPower       = "power"
	OOBSensorOther       = "other"

	OOBSensorOK       = "ok"
	OOBSensorWarning  = "warning"
	OOBSensorCritical = "critical"
	OOBSensorUnknown  = "unknown"
)

// Event-log severities (ReadOOBEventLog).
const (
	OOBEventInfo     = "info"
	OOBEventWarning  = "warning"
	OOBEventCritical = "critical"
	OOBEventUnknown  = "unknown"
)

type OOBProviderInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Version     string `json:"version"`
	Maturity    string `json:"maturity"` // Maturity* level (maturity.go)
	// ConfigFields are the per-device settings the admin device editor
	// renders (protocol, username, password, ports). Secret-typed values are
	// sealed at rest by the agent and opened per call.
	ConfigFields []ConfigField `json:"config_fields"`
	// Protocols is informational — "ipmi", "redfish", … shown in the driver
	// picker; routing never keys on it.
	Protocols []string `json:"protocols"`
	// Capability flags (§5) at driver level. These are what the agent gates
	// on: a control it offers a user is one it will call you for, and one it
	// hides is one it would have refused. Declare what the *driver* can do.
	// What one particular controller turned out to offer is the Probe answer,
	// which the agent caches for display and never turns into a refusal — a
	// device nobody has probed yet must still reach the RPCs its driver
	// declares (plugin-architecture.md decision 98). Refuse per device at
	// call time, with sdk.Refused, in the controller's own words.
	SupportsPower   bool `json:"supports_power"`
	SupportsSOL     bool `json:"supports_sol"`
	SupportsKVM     bool `json:"supports_kvm"`
	SupportsSensors bool `json:"supports_sensors"`
}

// OOBDevice identifies one device on every per-device call. Address is the
// inventory's dial target (host or host:port), first-class so drivers never
// parse it out of config and the device row stays the single source of what
// gets dialed. Config carries the driver-declared fields, secrets opened.
type OOBDevice struct {
	TenantID string
	DeviceID string
	Address  string
	Config   map[string]string
}

// OOBProbe is rendered by the panel, so its fields carry the wire names the
// rest of the API uses (the VMMetricSample convention).
type OOBProbe struct {
	Status          string            `json:"status"` // OOBProbe* value
	Detail          string            `json:"detail"` // human-readable, e.g. the auth error
	Vendor          string            `json:"vendor"`
	Model           string            `json:"model"`
	Firmware        string            `json:"firmware"`
	SupportsPower   bool              `json:"supports_power"`
	SupportsSOL     bool              `json:"supports_sol"`
	SupportsKVM     bool              `json:"supports_kvm"`
	SupportsSensors bool              `json:"supports_sensors"`
	Details         map[string]string `json:"details"` // plugin-defined facts, shown verbatim
}

type OOBPowerState struct {
	State   string            `json:"state"` // OOBPower* value
	Details map[string]string `json:"details"`
}

type OOBActionResult struct {
	Status        string `json:"status"` // OOBSucceeded / OOBFailed
	FailureReason string `json:"failure_reason"`
}

// OOBConsole carries everything the agent needs to bridge a browser
// websocket to the console websocket — the VMConsole shape (decision 10).
// The bridge splices bytes in both directions, which is what both console
// types are: a serial stream and RFB. WebsocketURL and Headers stay
// server-side. A driver whose protocol has no native websocket terminates it
// in-process and mints a one-time-ticketed loopback URL here.
type OOBConsole struct {
	Type         string // OOBConsoleTerm / OOBConsoleVNC
	WebsocketURL string
	Headers      map[string]string
	// Password answers the RFB handshake the browser runs inside the tunnel,
	// and belongs here only when it is a per-session grant that dies with the
	// session — a hypervisor's one-time vncproxy ticket is the shape this was
	// built for. A driver holding a credential that outlives the session, such
	// as a BMC's standing VNC-server password, must complete the handshake in
	// its own process and leave this empty instead: whatever is set here is
	// readable by the customer and keeps working after their access ends.
	Password string
	// CAPEM pins the CA the dial verifies WebsocketURL against; preferred
	// over InsecureSkipVerify, which is a lab-only opt-in ignored when a CA
	// is set.
	CAPEM              string
	InsecureSkipVerify bool
	ExpiresUnix        int64 // dial-by deadline for the one-time ticket; 0 = unknown
}

type OOBSensor struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"` // the BMC's own words, shown verbatim
	Kind   string  `json:"kind"`  // OOBSensor* kind
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`   // verbatim, e.g. "degrees C", "RPM"
	Status string  `json:"status"` // OOBSensor* status
}

type OOBEvent struct {
	TimeUnix int64  `json:"time_unix"` // 0 = unknown (dead RTC SELs are common)
	Severity string `json:"severity"`
	Message  string `json:"message"` // verbatim SEL text
}

// OOBProvider is what an out-of-band plugin implements. Every call is
// interactive (a human at a button or terminal): seconds-scale budgets.
// Declared refusals — a busy SOL session, an action this device cannot do —
// travel as sdk.Refused so the user reads the device's own words; transport
// problems stay plain errors and surface as "could not reach the device".
type OOBProvider interface {
	Describe(ctx context.Context) (OOBProviderInfo, error)
	Probe(ctx context.Context, dev OOBDevice) (OOBProbe, error)
	PowerState(ctx context.Context, dev OOBDevice) (OOBPowerState, error)
	PowerAction(ctx context.Context, dev OOBDevice, action string) (OOBActionResult, error)
	MintConsole(ctx context.Context, dev OOBDevice, kind, clientIP string) (OOBConsole, error)
	Sensors(ctx context.Context, dev OOBDevice) ([]OOBSensor, error)
	EventLog(ctx context.Context, dev OOBDevice, limit int) ([]OOBEvent, error)
}

// OOBDeviceDeclaration is what a driver can say about one device without
// dialing it: which consoles it offers there, as an OOBDeclared* answer per
// console. It is a pure function of the device's address and config — a
// "serial only" choice the operator made on the device, a controller family
// the driver knows has no VNC server — and the agent asks for it when the
// device is saved and again on every Test, storing the answer on the row.
// A declaration only ever subtracts from the driver's Describe flags: "yes"
// restores a console a probe had withdrawn, it never adds one the driver does
// not bridge. Leave a console undeclared and the agent keeps its probe rule
// for it (plugin-architecture.md §14c, decision 120).
type OOBDeviceDeclaration struct {
	SOL string `json:"sol"` // OOBDeclared* — the text console (OOBConsoleSOL)
	KVM string `json:"kvm"` // OOBDeclared* — the graphical console (OOBConsoleKVM)
}

// OOBDeviceDescriber is the optional half of the out-of-band contract a driver
// implements when it has something to declare per device. Answer from
// configuration alone — never dial — and in well under a second: the call sits
// on the device editor's save path. A driver without it declares nothing,
// which is the same as answering OOBDeviceDeclaration{} for every device.
type OOBDeviceDescriber interface {
	DescribeDevice(ctx context.Context, dev OOBDevice) (OOBDeviceDeclaration, error)
}

// DescribeOOBDevice asks a provider for its per-device declaration, answering
// "nothing declared" for one that does not implement OOBDeviceDescriber. The
// wire client always implements it, so a host uses this on whatever it holds
// — an in-process provider, a subprocess plugin, or a satellite's relay — and
// a plugin built before the RPC existed answers empty through it too.
func DescribeOOBDevice(ctx context.Context, p OOBProvider, dev OOBDevice) (OOBDeviceDeclaration, error) {
	if d, ok := p.(OOBDeviceDescriber); ok {
		return d.DescribeDevice(ctx, dev)
	}
	return OOBDeviceDeclaration{}, nil
}

// OOBPlugin adapts an OOBProvider across the go-plugin gRPC boundary.
type OOBPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl OOBProvider
}

func (p *OOBPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterOOBProviderServer(s, &oobGRPCServer{impl: p.Impl})
	return nil
}

func (p *OOBPlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &oobGRPCClient{client: proto.NewOOBProviderClient(c)}, nil
}

// ServeOOBProvider is the entry point for out-of-band plugin binaries.
func ServeOOBProvider(impl OOBProvider) { ServeBundle(Bundle{OOBProvider: impl}) }

// OOBProviderClient wraps a raw gRPC client into the Go interface — for
// hosts that dial the wire themselves (the satellite tunnel).
func OOBProviderClient(c proto.OOBProviderClient) OOBProvider {
	return &oobGRPCClient{client: c}
}

// OOBProviderServer adapts an implementation into the wire server — for
// relays that serve the contract over their own listener (the satellite
// tunnel's per-call delegation).
func OOBProviderServer(impl OOBProvider) proto.OOBProviderServer {
	return &oobGRPCServer{impl: impl}
}

// --- gRPC adapters ---

func oobDeviceFromProto(req *proto.OOBDeviceRequest) OOBDevice {
	if req == nil {
		return OOBDevice{}
	}
	return OOBDevice{TenantID: req.TenantId, DeviceID: req.DeviceId, Address: req.Address, Config: req.Config}
}

func oobDeviceToProto(dev OOBDevice) *proto.OOBDeviceRequest {
	return &proto.OOBDeviceRequest{TenantId: dev.TenantID, DeviceId: dev.DeviceID, Address: dev.Address, Config: dev.Config}
}

type oobGRPCServer struct {
	proto.UnimplementedOOBProviderServer
	impl OOBProvider
}

func (s *oobGRPCServer) DescribeOOBProvider(ctx context.Context, _ *proto.DescribeOOBProviderRequest) (*proto.DescribeOOBProviderResponse, error) {
	info, err := s.impl.Describe(ctx)
	if err != nil {
		return nil, err
	}
	fields := configFieldsToProto(info.ConfigFields)
	return &proto.DescribeOOBProviderResponse{
		Name: info.Name, DisplayName: info.DisplayName, Version: info.Version, Maturity: info.Maturity,
		ConfigFields: fields, Protocols: info.Protocols,
		SupportsPower: info.SupportsPower, SupportsSol: info.SupportsSOL,
		SupportsKvm: info.SupportsKVM, SupportsSensors: info.SupportsSensors,
	}, nil
}

func (s *oobGRPCServer) ProbeOOBDevice(ctx context.Context, req *proto.OOBDeviceRequest) (*proto.ProbeOOBDeviceResponse, error) {
	p, err := s.impl.Probe(ctx, oobDeviceFromProto(req))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.ProbeOOBDeviceResponse{
		Status: p.Status, Detail: p.Detail, Vendor: p.Vendor, Model: p.Model, Firmware: p.Firmware,
		SupportsPower: p.SupportsPower, SupportsSol: p.SupportsSOL,
		SupportsKvm: p.SupportsKVM, SupportsSensors: p.SupportsSensors,
		Details: p.Details,
	}, nil
}

func (s *oobGRPCServer) GetOOBPowerState(ctx context.Context, req *proto.OOBDeviceRequest) (*proto.OOBPowerStateResponse, error) {
	st, err := s.impl.PowerState(ctx, oobDeviceFromProto(req))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.OOBPowerStateResponse{State: st.State, Details: st.Details}, nil
}

func (s *oobGRPCServer) OOBPowerAction(ctx context.Context, req *proto.OOBPowerActionRequest) (*proto.OOBPowerActionResponse, error) {
	res, err := s.impl.PowerAction(ctx, oobDeviceFromProto(req.GetDevice()), req.GetAction())
	if err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.OOBPowerActionResponse{Status: res.Status, FailureReason: res.FailureReason}, nil
}

func (s *oobGRPCServer) MintOOBConsole(ctx context.Context, req *proto.MintOOBConsoleRequest) (*proto.OOBConsoleResponse, error) {
	c, err := s.impl.MintConsole(ctx, oobDeviceFromProto(req.GetDevice()), req.GetKind(), req.GetClientIp())
	if err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.OOBConsoleResponse{
		Type: c.Type, WebsocketUrl: c.WebsocketURL, Headers: c.Headers,
		Password: c.Password, CaPem: c.CAPEM,
		InsecureSkipVerify: c.InsecureSkipVerify, ExpiresUnix: c.ExpiresUnix,
	}, nil
}

func (s *oobGRPCServer) ReadOOBSensors(ctx context.Context, req *proto.OOBDeviceRequest) (*proto.OOBSensorsResponse, error) {
	sensors, err := s.impl.Sensors(ctx, oobDeviceFromProto(req))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	out := make([]*proto.OOBSensor, len(sensors))
	for i, sn := range sensors {
		out[i] = &proto.OOBSensor{Id: sn.ID, Label: sn.Label, Kind: sn.Kind, Value: sn.Value, Unit: sn.Unit, Status: sn.Status}
	}
	return &proto.OOBSensorsResponse{Sensors: out}, nil
}

func (s *oobGRPCServer) ReadOOBEventLog(ctx context.Context, req *proto.OOBEventLogRequest) (*proto.OOBEventLogResponse, error) {
	events, err := s.impl.EventLog(ctx, oobDeviceFromProto(req.GetDevice()), int(req.GetLimit()))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	out := make([]*proto.OOBEvent, len(events))
	for i, e := range events {
		out[i] = &proto.OOBEvent{TimeUnix: e.TimeUnix, Severity: e.Severity, Message: e.Message}
	}
	return &proto.OOBEventLogResponse{Events: out}, nil
}

// DescribeOOBDevice answers empty — nothing declared — for an implementation
// without OOBDeviceDescriber rather than Unimplemented: that is the truthful
// answer, and it keeps the wire's two "nothing to say" cases (a driver with
// no opinion, a driver built before the RPC) reading the same to the host.
func (s *oobGRPCServer) DescribeOOBDevice(ctx context.Context, req *proto.OOBDeviceRequest) (*proto.DescribeOOBDeviceResponse, error) {
	decl, err := DescribeOOBDevice(ctx, s.impl, oobDeviceFromProto(req))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.DescribeOOBDeviceResponse{Sol: decl.SOL, Kvm: decl.KVM}, nil
}

type oobGRPCClient struct {
	client proto.OOBProviderClient
}

func (c *oobGRPCClient) Describe(ctx context.Context) (OOBProviderInfo, error) {
	resp, err := c.client.DescribeOOBProvider(ctx, &proto.DescribeOOBProviderRequest{})
	if err != nil {
		return OOBProviderInfo{}, err
	}
	fields := configFieldsFromProto(resp.ConfigFields)
	return OOBProviderInfo{
		Name: resp.Name, DisplayName: resp.DisplayName, Version: resp.Version, Maturity: resp.Maturity,
		ConfigFields: fields, Protocols: resp.Protocols,
		SupportsPower: resp.SupportsPower, SupportsSOL: resp.SupportsSol,
		SupportsKVM: resp.SupportsKvm, SupportsSensors: resp.SupportsSensors,
	}, nil
}

func (c *oobGRPCClient) Probe(ctx context.Context, dev OOBDevice) (OOBProbe, error) {
	resp, err := c.client.ProbeOOBDevice(ctx, oobDeviceToProto(dev))
	if err != nil {
		return OOBProbe{}, refusedFromStatus(err)
	}
	return OOBProbe{
		Status: resp.Status, Detail: resp.Detail, Vendor: resp.Vendor, Model: resp.Model, Firmware: resp.Firmware,
		SupportsPower: resp.SupportsPower, SupportsSOL: resp.SupportsSol,
		SupportsKVM: resp.SupportsKvm, SupportsSensors: resp.SupportsSensors,
		Details: resp.Details,
	}, nil
}

func (c *oobGRPCClient) PowerState(ctx context.Context, dev OOBDevice) (OOBPowerState, error) {
	resp, err := c.client.GetOOBPowerState(ctx, oobDeviceToProto(dev))
	if err != nil {
		return OOBPowerState{}, refusedFromStatus(err)
	}
	return OOBPowerState{State: resp.State, Details: resp.Details}, nil
}

func (c *oobGRPCClient) PowerAction(ctx context.Context, dev OOBDevice, action string) (OOBActionResult, error) {
	resp, err := c.client.OOBPowerAction(ctx, &proto.OOBPowerActionRequest{Device: oobDeviceToProto(dev), Action: action})
	if err != nil {
		return OOBActionResult{}, refusedFromStatus(err)
	}
	return OOBActionResult{Status: resp.Status, FailureReason: resp.FailureReason}, nil
}

func (c *oobGRPCClient) MintConsole(ctx context.Context, dev OOBDevice, kind, clientIP string) (OOBConsole, error) {
	resp, err := c.client.MintOOBConsole(ctx, &proto.MintOOBConsoleRequest{Device: oobDeviceToProto(dev), Kind: kind, ClientIp: clientIP})
	if err != nil {
		return OOBConsole{}, refusedFromStatus(err)
	}
	return OOBConsole{
		Type: resp.Type, WebsocketURL: resp.WebsocketUrl, Headers: resp.Headers,
		Password: resp.Password, CAPEM: resp.CaPem,
		InsecureSkipVerify: resp.InsecureSkipVerify, ExpiresUnix: resp.ExpiresUnix,
	}, nil
}

func (c *oobGRPCClient) Sensors(ctx context.Context, dev OOBDevice) ([]OOBSensor, error) {
	resp, err := c.client.ReadOOBSensors(ctx, oobDeviceToProto(dev))
	if err != nil {
		return nil, refusedFromStatus(err)
	}
	out := make([]OOBSensor, len(resp.Sensors))
	for i, sn := range resp.Sensors {
		out[i] = OOBSensor{ID: sn.Id, Label: sn.Label, Kind: sn.Kind, Value: sn.Value, Unit: sn.Unit, Status: sn.Status}
	}
	return out, nil
}

func (c *oobGRPCClient) EventLog(ctx context.Context, dev OOBDevice, limit int) ([]OOBEvent, error) {
	resp, err := c.client.ReadOOBEventLog(ctx, &proto.OOBEventLogRequest{Device: oobDeviceToProto(dev), Limit: int32(limit)})
	if err != nil {
		return nil, refusedFromStatus(err)
	}
	out := make([]OOBEvent, len(resp.Events))
	for i, e := range resp.Events {
		out[i] = OOBEvent{TimeUnix: e.TimeUnix, Severity: e.Severity, Message: e.Message}
	}
	return out, nil
}

// The wire client always speaks the declaration, so DescribeOOBDevice works
// on it whatever the plugin on the far end was built against.
var _ OOBDeviceDescriber = (*oobGRPCClient)(nil)

// DescribeDevice asks the plugin for its per-device declaration. A plugin
// built before contract 4 answers the RPC with Unimplemented, which is the
// same fact as an empty declaration — nothing to say about this device — and
// is folded into one so a host never has to know which it got.
func (c *oobGRPCClient) DescribeDevice(ctx context.Context, dev OOBDevice) (OOBDeviceDeclaration, error) {
	resp, err := c.client.DescribeOOBDevice(ctx, oobDeviceToProto(dev))
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
			return OOBDeviceDeclaration{}, nil
		}
		return OOBDeviceDeclaration{}, refusedFromStatus(err)
	}
	return OOBDeviceDeclaration{SOL: resp.Sol, KVM: resp.Kvm}, nil
}
