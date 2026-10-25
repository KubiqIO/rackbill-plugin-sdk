package plugin

import (
	"context"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"kubiq.io/rackbill/sdk/plugin/proto"
)

// ProvisionerPluginName is the key provisioners are served and dispensed under.
const ProvisionerPluginName = "provisioner"

const (
	ProvisionSucceeded = "succeeded"
	ProvisionFailed    = "failed"
)

// VM power states reported by GetVMState.
const (
	VMRunning = "running"
	VMStopped = "stopped"
	VMPaused  = "paused"
	VMUnknown = "unknown"
)

// VM power actions accepted by VMPowerAction.
const (
	VMActionStart    = "start"
	VMActionShutdown = "shutdown" // graceful, via guest
	VMActionStop     = "stop"     // hard power-off
	VMActionReboot   = "reboot"
)

// Metric timeframes accepted by GetVMMetrics.
const (
	VMTimeframeCurrent = "current"
	VMTimeframeHour    = "hour"
	VMTimeframeDay     = "day"
	VMTimeframeWeek    = "week"
)

// ConfigField describes one admin-edited setting the plugin needs; the admin
// panel renders these dynamically.
type ConfigField struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"` // "text", "textarea" (multi-line, e.g. PEM), "number", "secret", "select" (one of Options), "checkbox" (a yes/no flag)
	Required bool   `json:"required"`
	// Scope is provisioner-only: where one setting is entered, and so what
	// kind of thing this integration is placed on. "product" (the default)
	// is the workload — plan, template, sizes — edited on each product.
	// "node" is the infrastructure — API URL, credentials, hypervisor node —
	// rendered in the admin node editor, and declaring even one node-scoped
	// field is what makes the agent place this provisioner's services on
	// registered nodes (PlacesOnNodes): the guided setup asks for a node
	// group and a node, the allocator picks among them, and a product with
	// no node to land on is not sellable. "tenant" is the account — the
	// credentials of a hosted service with one endpoint that scales its own
	// backends, entered once per company and applied to every product the
	// integration delivers; a provisioner whose fields are only product- and
	// tenant-scoped never sees a node. On every call the plugin receives one
	// map: the product's config, the service's option values over it, the
	// tenant settings over those, and the placed node's config over all —
	// so a product row can no more override the account's key than the
	// node's address. Other kinds ignore it.
	Scope string `json:"scope,omitempty"`
	// DynamicOptions: the plugin can enumerate valid values for this field
	// via ListConfigOptions once the fields named in OptionsDependOn are
	// filled in. Provisioners, notifiers and, since contract 15, accounting
	// product fields honour it. The panel renders a dropdown fed by that
	// call, falling back to free text when enumeration fails.
	DynamicOptions  bool     `json:"dynamic_options,omitempty"`
	OptionsDependOn []string `json:"options_depend_on,omitempty"`
	// Spec is provisioner-only: the workload dimension and unit this field
	// sizes (one of the Spec* constants), so the admin panel derives the
	// allocator's resource footprint — cores, memory, disk — from the
	// workload fields instead of asking for the same numbers twice in
	// different units. Empty means the field sizes nothing.
	Spec string `json:"spec,omitempty"`
	// Destination marks a field that decides *where* this config's secrets
	// are sent: the base URL, host, port or endpoint they are presented to,
	// the environment/sandbox switch that chooses between them, and the TLS
	// trust settings (ca_pem, insecure_tls) that decide who is allowed to
	// answer. Every kind honours it.
	//
	// The agent lets an admin form resubmit a secret field blank to mean
	// "keep the stored one", and it keeps that promise only while every
	// Destination field still holds the value the secret was entered for —
	// otherwise a stored credential would be opened and handed to whatever
	// the new address answers. Marking a field therefore costs the operator
	// a re-entry of the secret whenever they change it, so mark the fields
	// that move the call and no others: what the request *asks for* (a plan,
	// a template, a threshold, a from-name) is not a destination.
	//
	// Plugins whose endpoint is fixed in code mark nothing. The agent cannot
	// tell that apart from a plugin written before this field existed, so it
	// falls back to treating every non-secret field as a destination — safe,
	// but it asks for the secret again on any edit.
	Destination bool `json:"destination,omitempty"`
	// Options is the fixed set of values a "select" field accepts, declared
	// in the order the panel should offer them; every kind honours it. Each
	// option's Description is the explanation an admin gets on hover and
	// under the control once it is chosen, so the choice is made in the
	// panel rather than by reading plugin source: "gmail: authenticated
	// submission via smtp.gmail.com, needs an app password" beside "relay:
	// the Workspace relay, authorized by IP". The option whose Value is ""
	// stands for the field's default — what the plugin does when the key is
	// absent from its config — and the panel shows it for an unset field;
	// without one the panel adds a blank "Select…" entry and the field stays
	// unset until the admin picks. A stored value that matches no option is
	// still shown (labeled as the saved value) so an upgrade that renames a
	// choice never silently rewrites a config. Ignored for other types.
	Options []ConfigOption `json:"options,omitempty"`
	// DefaultChecked is checkbox-only: the state an unset field shows, which
	// is what the plugin does when the key is absent from its config. The
	// panel reads a stored value as "1" = on, "0" = off, "" = this default,
	// and anything else typed under an older text label ("true", "yes") as
	// on; a toggle writes "1" or "0" for the non-default state and "" for
	// the default one, so an unset flag stays absent across a save and a
	// Destination flag (insecure_tls) is not a moved destination the first
	// time an older config is re-saved; a value the admin never touches is
	// resubmitted as stored. Parse a flag as "1" = on, "0" = off, "" = the
	// default. Ignored for other types.
	DefaultChecked bool `json:"default_checked,omitempty"`
	// MinLength, MaxLength and Pattern are the limits the remote system puts
	// on a text-like value (lengths in characters, Pattern an RE2 expression
	// matched against the whole value); Min and Max bound a number field.
	// They exist because a remote API's own description rarely states them:
	// declared here, the agent refuses the value at save — product, node,
	// option default and every option choice — instead of the remote
	// refusing it after the customer paid. Zero/nil means no limit; an empty
	// value is Required's business. An agent that predates these ignores
	// them, so a plugin still runs CheckValue on what it receives.
	MinLength int    `json:"min_length,omitempty"`
	MaxLength int    `json:"max_length,omitempty"`
	Pattern   string `json:"pattern,omitempty"`
	Min       *int64 `json:"min,omitempty"`
	Max       *int64 `json:"max,omitempty"`
	// Help is one line under the input saying what the setting does; the
	// panel appends the declared limits.
	Help string `json:"help,omitempty"`
	// Sensitive marks a workload value that may carry credentials, such as
	// cloud-init user data: the host seals it at rest like a secret, withholds
	// it on read, keeps it off invoices and service views, and redacts it
	// from logs. Unlike a secret it may be any type (a textarea) and may be
	// offered at checkout as an option. Contract 7.
	Sensitive bool `json:"sensitive,omitempty"`
}

// configOptionsToProto and configOptionsFromProto carry the option list for
// both places it travels: a select field's fixed choices in Describe, and an
// enumerable field's live ones in ListConfigOptions.
func configOptionsToProto(opts []ConfigOption) []*proto.ConfigFieldOption {
	if len(opts) == 0 {
		return nil
	}
	out := make([]*proto.ConfigFieldOption, len(opts))
	for i, o := range opts {
		out[i] = &proto.ConfigFieldOption{Value: o.Value, Label: o.Label, Description: o.Description}
	}
	return out
}

func configOptionsFromProto(opts []*proto.ConfigFieldOption) []ConfigOption {
	if len(opts) == 0 {
		return nil
	}
	out := make([]ConfigOption, len(opts))
	for i, o := range opts {
		out[i] = ConfigOption{Value: o.Value, Label: o.Label, Description: o.Description}
	}
	return out
}

// configFieldsToProto and configFieldsFromProto are the one place a
// ConfigField crosses the wire, shared by every kind that declares one. The
// type is common to all of them, so a per-kind copy of this loop is how a
// new member gets carried for the kind that added it and silently dropped
// for the other eleven — which is what happened to Scope and Spec, and what
// Destination cannot afford: a field the agent never sees is a field it
// treats as absent, and absent means "this plugin declared no destination".
func configFieldsToProto(fields []ConfigField) []*proto.ConfigField {
	out := make([]*proto.ConfigField, len(fields))
	for i, f := range fields {
		out[i] = &proto.ConfigField{
			Key: f.Key, Label: f.Label, Type: f.Type, Required: f.Required, Scope: f.Scope,
			DynamicOptions: f.DynamicOptions, OptionsDependOn: f.OptionsDependOn,
			Spec: f.Spec, Destination: f.Destination, Options: configOptionsToProto(f.Options),
			DefaultChecked: f.DefaultChecked,
			MinLength:      int32(f.MinLength), MaxLength: int32(f.MaxLength), Pattern: f.Pattern,
			HasMin: f.Min != nil, Min: deref(f.Min), HasMax: f.Max != nil, Max: deref(f.Max),
			Help: f.Help, Sensitive: f.Sensitive,
		}
	}
	return out
}

func configFieldsFromProto(fields []*proto.ConfigField) []ConfigField {
	out := make([]ConfigField, len(fields))
	for i, f := range fields {
		out[i] = ConfigField{
			Key: f.Key, Label: f.Label, Type: f.Type, Required: f.Required, Scope: f.Scope,
			DynamicOptions: f.DynamicOptions, OptionsDependOn: f.OptionsDependOn,
			Spec: f.Spec, Destination: f.Destination, Options: configOptionsFromProto(f.Options),
			DefaultChecked: f.DefaultChecked,
			MinLength:      int(f.MinLength), MaxLength: int(f.MaxLength), Pattern: f.Pattern,
			Min: bound(f.HasMin, f.Min), Max: bound(f.HasMax, f.Max),
			Help: f.Help, Sensitive: f.Sensitive,
		}
	}
	return out
}

func deref(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func bound(set bool, v int64) *int64 {
	if !set {
		return nil
	}
	return &v
}

// ConfigField.Spec values: what a workload field measures, and in what unit.
const (
	SpecCPUCores   = "cpu_cores"
	SpecCPUPercent = "cpu_pct" // 100 = one core
	SpecMemoryMB   = "memory_mb"
	SpecMemoryGB   = "memory_gb"
	SpecDiskMB     = "disk_mb"
	SpecDiskGB     = "disk_gb"
)

// ConfigField scopes. ScopeProduct is what an empty Scope means.
const (
	ScopeProduct = "product"
	ScopeNode    = "node"
	ScopeTenant  = "tenant"
)

// EffectiveScope is the field's scope with the default spelled out: an
// unset Scope means the product (plugin architecture §7.1).
func (f ConfigField) EffectiveScope() string {
	if f.Scope == "" {
		return ScopeProduct
	}
	return f.Scope
}

// HasScope reports whether any of the fields is declared in scope.
func HasScope(fields []ConfigField, scope string) bool {
	for _, f := range fields {
		if f.EffectiveScope() == scope {
			return true
		}
	}
	return false
}

type ProvisionerInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	// Version is informational. Plugins distributed through the RackBill
	// marketplace leave it empty: their version is assigned by the release
	// train that ships them (the catalog is authoritative), so a value here
	// could only disagree with it. Out-of-band plugins may use it freely.
	Version      string        `json:"version"`
	Maturity     string        `json:"maturity"` // Maturity* level (maturity.go)
	ConfigFields []ConfigField `json:"config_fields"`
	// Capability flags (§5): the host only calls VM RPCs when these are set.
	SupportsVM      bool `json:"supports_vm"`
	SupportsConsole bool `json:"supports_console"`
	// SupportsPasswordReset gates VMResetPassword (VMPasswordResetter): the
	// customer can set a new root password and see it once.
	SupportsPasswordReset bool `json:"supports_password_reset"`
	// SupportsReinstall gates VMReinstall (VMReinstaller): the customer can
	// wipe the guest and install the image named in config under
	// ReinstallKey, the key the agent writes the customer's pick into.
	SupportsReinstall bool   `json:"supports_reinstall"`
	ReinstallKey      string `json:"reinstall_key,omitempty"`
	// MetricsWindowSeconds is how far back VMMetrics can reach when the
	// provider keeps only a short recent window; the panel then shows that
	// window instead of the hour, day and week choices. 0 means every
	// timeframe is served in full.
	MetricsWindowSeconds int `json:"metrics_window_seconds,omitempty"`
	// ReinstallAppKey is a second reinstall source: the config key naming an
	// application template. The agent offers the service's option for it
	// beside ReinstallKey's; a pick from it is written here with ReinstallKey
	// cleared, and an image pick clears this key, so exactly one is set.
	ReinstallAppKey string `json:"reinstall_app_key,omitempty"`
	// SupportsHostname gates VMSetHostname (VMHostnameSetter).
	SupportsHostname bool `json:"supports_hostname"`
	// SupportsNetwork gates VMAddresses (VMNetworkReader); SupportsReverseDNS
	// gates VMSetReverseDNS (VMReverseDNSSetter) and implies it.
	SupportsNetwork    bool `json:"supports_network"`
	SupportsReverseDNS bool `json:"supports_reverse_dns"`
	// SupportsGroups gates VMGroups and VMSetGroup (VMGrouper).
	SupportsGroups bool `json:"supports_groups"`
	// SupportsSSHKeys: Provision and VMReinstall read SSHKeys, the customer's
	// saved keys they chose at checkout or on reinstall.
	SupportsSSHKeys bool `json:"supports_ssh_keys"`
	// Contract 10 (vm_features.go): private networks (VMPrivateNetworker),
	// the provider's own backups (VMBackupper), boot modes (VMBootModer) and
	// customer ISOs (VMISOManager, implies SupportsBootModes).
	SupportsPrivateNetworks bool `json:"supports_private_networks"`
	SupportsProviderBackups bool `json:"supports_provider_backups"`
	SupportsBootModes       bool `json:"supports_boot_modes"`
	SupportsISOImages       bool `json:"supports_iso_images"`
	// Backup capability: the provisioner can snapshot its own guest into a
	// target minted by a backup repository plugin. Restore is separate —
	// producing a backup and putting one back are different privileges.
	SupportsBackup        bool `json:"supports_backup"`
	SupportsRestoreBackup bool `json:"supports_restore_backup"`
	// Capacity capability: GetNodeCapacity works against node-scoped config.
	SupportsCapacity bool `json:"supports_capacity"`
	// RequiresDomain: Provision needs the service label to be the domain to
	// host (web hosting panels). The agent validates the label as an fqdn at
	// order time and the order form collects it as a required domain field.
	RequiresDomain bool `json:"requires_domain"`
	// Panel access capability: the remote system is a customer-facing control
	// panel. SupportsPanelLogin gates PanelLoginURL (single-use SSO links),
	// SupportsPanelUsage gates PanelUsage (current used/limit gauges). The
	// two are independent — an integration may report usage without an SSO
	// mechanism or the reverse.
	SupportsPanelLogin bool `json:"supports_panel_login"`
	SupportsPanelUsage bool `json:"supports_panel_usage"`
	// Renewal capability: the remote object carries its own paid-through date
	// (a licence expiry, a seat term) that must move when the customer pays
	// for another period. Gates RenewService; provisioners without it are
	// never told about a renewal, because for a machine or a hosting account
	// there is nothing to tell.
	SupportsRenew bool `json:"supports_renew"`
	// Game server capability: the remote system is a game panel whose
	// end-user surface (console, files, backups, databases, schedules,
	// network, sub-users, startup, settings, activity) the portal
	// reproduces. Gates the Game* RPCs; implies SupportsVM, which carries
	// state and power.
	SupportsGameServer bool `json:"supports_game_server"`
	// NodeDescription says, in one sentence, what one registered node *is*
	// for this integration — "one Pterodactyl location; the panel picks the
	// machine inside it", "one Proxmox VE node" — for the admin's node
	// editor and the guided setup. Empty when the provisioner declares no
	// node-scoped fields (PlacesOnNodes is false).
	NodeDescription string `json:"node_description,omitempty"`
}

// PlacesOnNodes reports whether the agent places this provisioner's
// services on registered nodes: it does exactly when a node-scoped config
// field is declared (ConfigField.Scope). The guided setup's node steps,
// the node editor's provisioner list, product readiness and the
// enumeration borrow all read this one rule.
func (i ProvisionerInfo) PlacesOnNodes() bool {
	return HasScope(i.ConfigFields, ScopeNode)
}

// HasTenantSettings reports whether this provisioner connects at the
// account level: it declares tenant-scoped config fields, entered once per
// company under the admin's integration settings and merged into every
// call.
func (i ProvisionerInfo) HasTenantSettings() bool {
	return HasScope(i.ConfigFields, ScopeTenant)
}

type ProvisionInput struct {
	TenantID  string
	ServiceID string
	AccountID string
	// AccountEmail and AccountName are the owning account's primary contact
	// (the owner member's login email, the account's display name), for
	// integrations that create a per-customer login in the remote system.
	// Either may be empty; plugins must degrade gracefully, never fabricate
	// an address.
	AccountEmail string
	AccountName  string
	Label        string
	Config       map[string]string
	// PeriodEnd is the end of the period this order paid for (the service's
	// next due date), for remote objects that carry their own paid-through
	// date. Zero when the service has no cycle (one-time purchases) or the
	// host predates the field; RenewService moves it on every later renewal.
	PeriodEnd time.Time
	// SSHKeys are the public keys the customer chose to install, sent only to
	// provisioners that declare SupportsSSHKeys. Empty when none were chosen.
	SSHKeys []SSHKey
	// AccountSSHKeys is every key saved on the account, for an integration
	// that keeps a per-customer key list and fills it when it creates the
	// customer's remote login. Install only SSHKeys on the server.
	AccountSSHKeys []SSHKey
}

// SSHKey is one OpenSSH public key a customer saved on their account.
type SSHKey struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

type ProvisionResult struct {
	Status        string
	ExternalRef   string
	FailureReason string
	Details       map[string]string
}

// ServiceActionInput identifies an already-provisioned service for
// suspend/unsuspend/terminate calls.
type ServiceActionInput struct {
	TenantID    string
	ServiceID   string
	ExternalRef string
	Config      map[string]string
}

type ServiceActionResult struct {
	Status        string
	FailureReason string
}

// ServiceRenewInput tells a provisioner that the customer paid for another
// period: the ServiceActionInput shape plus the date they are now paid
// through. (Distinct from the registrar kind's RenewInput, which renews a
// domain registration for a number of years.)
type ServiceRenewInput struct {
	TenantID    string
	ServiceID   string
	ExternalRef string
	Config      map[string]string
	// PeriodEnd is the service's new next due date — the moment the customer
	// stops being paid up. Always set: the host never sends a renewal for a
	// service without one.
	PeriodEnd time.Time
}

// ServiceRenewer is the optional renewal capability of a provisioner.
// Implement it alongside Provisioner and set SupportsRenew in Describe; the
// host never calls it without the flag, and never enqueues the work at all
// for provisioners that lack it.
//
// Only integrations whose remote object holds its own paid-through date need
// this. It is a *notification*, not a state change: the service is already
// active and stays that way, so an implementation moves an expiry (or does
// nothing and succeeds) and never re-provisions. Like every other lifecycle
// call it must be idempotent — the same period may be delivered twice.
type ServiceRenewer interface {
	RenewService(ctx context.Context, in ServiceRenewInput) (ServiceActionResult, error)
}

// Provisioner is what a provisioning plugin implements.
type Provisioner interface {
	Describe(ctx context.Context) (ProvisionerInfo, error)
	Provision(ctx context.Context, in ProvisionInput) (ProvisionResult, error)
	Suspend(ctx context.Context, in ServiceActionInput) (ServiceActionResult, error)
	Unsuspend(ctx context.Context, in ServiceActionInput) (ServiceActionResult, error)
	Terminate(ctx context.Context, in ServiceActionInput) (ServiceActionResult, error)
}

// VMRef identifies a provisioned virtual machine on VM-capability calls; the
// same shape as ServiceActionInput.
type VMRef struct {
	TenantID    string
	ServiceID   string
	ExternalRef string
	Config      map[string]string
	// SSHKeys is set on VMReinstall only, for SupportsSSHKeys plugins.
	SSHKeys []SSHKey
}

type VMState struct {
	State         string            // VMRunning, VMStopped, VMPaused, VMUnknown
	UptimeSeconds int64             // 0 unless running
	Details       map[string]string // ip, hostname, node, ...
}

// VMMetricSample is one timestamped resource sample. Absolute values plus
// totals — utilisation ratios are derived by consumers, and a future usage
// collector can bill on the raw quantities.
type VMMetricSample struct {
	UnixSeconds    int64   `json:"unix_seconds"`
	CPUPct         float64 `json:"cpu_pct"`
	MemBytes       int64   `json:"mem_bytes"`
	MemTotalBytes  int64   `json:"mem_total_bytes"`
	DiskBytes      int64   `json:"disk_bytes"`
	DiskTotalBytes int64   `json:"disk_total_bytes"`
	NetInBps       int64   `json:"net_in_bps"`
	NetOutBps      int64   `json:"net_out_bps"`
}

// VMConsole is what the agent needs to bridge a browser to the hypervisor's
// console websocket. WebsocketURL and Headers stay server-side; Password goes
// to the browser's VNC client for the in-tunnel RFB handshake.
type VMConsole struct {
	Type         string // "vnc"
	WebsocketURL string
	Headers      map[string]string
	Password     string
	// CAPEM pins the CA the agent verifies WebsocketURL against; preferred
	// over InsecureSkipVerify, which is a lab-only opt-in ignored when a CA
	// is provided.
	CAPEM              string
	InsecureSkipVerify bool
	ExpiresUnix        int64
}

// VMManager is the optional VM capability of a provisioner. Implement it
// alongside Provisioner and set SupportsVM (and SupportsConsole if VMConsole
// works) in Describe. The host never calls these without the flag.
type VMManager interface {
	VMState(ctx context.Context, ref VMRef) (VMState, error)
	VMPower(ctx context.Context, ref VMRef, action string) (ServiceActionResult, error)
	VMMetrics(ctx context.Context, ref VMRef, timeframe string) ([]VMMetricSample, error)
	VMConsole(ctx context.Context, ref VMRef) (VMConsole, error)
}

// VMPasswordResetter is the optional root-password reset of a VM-capable
// provisioner. Implement it and set SupportsPasswordReset in Describe. The
// returned password is the guest's new root credential: the host shows it to
// the customer once, over the request that asked, and never stores, logs or
// emails it — so a plugin must not log it either.
type VMPasswordResetter interface {
	VMResetPassword(ctx context.Context, ref VMRef) (string, error)
}

// VMReinstaller is the optional reinstall of a VM-capable provisioner.
// Implement it and set SupportsReinstall and ReinstallKey in Describe. It
// wipes the guest and installs the image ref.Config[ReinstallKey] names (the
// agent has written the customer's pick there). A returned password is shown
// once like a reset's; "" means the provider sets none and the customer uses
// the password reset once the install finishes. Never log the password.
type VMReinstaller interface {
	VMReinstall(ctx context.Context, ref VMRef) (string, error)
}

// VMHostnameSetter is the optional rename of a VM-capable provisioner: the
// server's hostname on the provider. Implement it and set SupportsHostname.
// The agent has checked the name is an RFC 1123 hostname; a provider's own
// refusal travels as a RefusedError (Refused).
type VMHostnameSetter interface {
	VMSetHostname(ctx context.Context, ref VMRef, hostname string) error
}

// VMAddress is one address on a server. A single address has an empty
// Range; an assigned block (an IPv6 /64) carries its CIDR there, and its
// reverse DNS is per address inside the block, which this contract never
// edits (ReverseDNSEditable is false).
type VMAddress struct {
	Address            string `json:"address"`
	Range              string `json:"range,omitempty"`
	Primary            bool   `json:"primary"`
	ReverseDNS         string `json:"reverse_dns"`
	ReverseDNSEditable bool   `json:"reverse_dns_editable"`
}

// VMNetworkReader is the optional address listing of a VM-capable
// provisioner. Implement it and set SupportsNetwork. The agent shows the
// list and records the addresses in IPAM as provider-assigned.
type VMNetworkReader interface {
	VMAddresses(ctx context.Context, ref VMRef) ([]VMAddress, error)
}

// VMReverseDNSSetter sets the PTR record of one single address that
// VMAddresses marked editable. Implement it and set SupportsReverseDNS.
type VMReverseDNSSetter interface {
	VMSetReverseDNS(ctx context.Context, ref VMRef, address, domain string) error
}

// VMGroup is one provider-side group of servers (a SolusVM project).
type VMGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// VMGroups is the server owner's groups and which one holds the server.
type VMGroups struct {
	CurrentID string    `json:"current_id"`
	Groups    []VMGroup `json:"groups"`
	// CanCreate reports that VMSetGroup accepts a new name.
	CanCreate bool `json:"can_create"`
}

// VMGrouper is the optional grouping of a VM-capable provisioner. Implement
// it and set SupportsGroups. VMSetGroup takes exactly one of groupID (one of
// VMGroups' own) or newName (create a group for the server's owner, then
// move the server into it) and returns the group the server is now in.
type VMGrouper interface {
	VMGroups(ctx context.Context, ref VMRef) (VMGroups, error)
	VMSetGroup(ctx context.Context, ref VMRef, groupID, newName string) (VMGroup, error)
}

// ServiceBackupInput asks a provisioner to snapshot the service it provisioned
// into Target — space a backup repository plugin already prepared.
type ServiceBackupInput struct {
	TenantID    string
	ServiceID   string
	ExternalRef string
	Config      map[string]string
	Target      BackupTarget
	Label       string
	Options     map[string]string
}

type ServiceRestoreInput struct {
	TenantID    string
	ServiceID   string
	ExternalRef string
	Config      map[string]string
	Target      BackupTarget
	SnapshotID  string
	Options     map[string]string
}

// NodeCapacity is what one registered node can hold: totals plus FOREIGN
// commitments — resources promised to guests the platform does not manage.
// The agent adds its own placements from its database, so implementations
// must exclude platform-managed guests from the foreign figures. A zero
// total means unknown; the allocator skips that dimension.
type NodeCapacity struct {
	CPUCores         int64             `json:"cpu_cores"`
	MemTotalBytes    int64             `json:"mem_total_bytes"`
	DiskTotalBytes   int64             `json:"disk_total_bytes"`
	ForeignCPUCores  int64             `json:"foreign_cpu_cores"`
	ForeignMemBytes  int64             `json:"foreign_mem_bytes"`
	ForeignDiskBytes int64             `json:"foreign_disk_bytes"`
	GuestCount       int64             `json:"guest_count"`
	Details          map[string]string `json:"details,omitempty"`
	// Usage is the node's live utilization at probe time — what the hardware
	// is actually doing, as opposed to what has been promised. Optional: a
	// provisioner whose API exposes no live host load leaves it nil and the
	// panel shows allocation only. Set it only from a real measurement,
	// never from allocation sums.
	Usage *NodeUsage `json:"usage,omitempty"`
	// Advisory says the upstream places guests itself and refuses what does
	// not fit (a game panel's deploy viability, SolusVM's scheduler), so the
	// figures inform rather than bound. They are still recorded and forecast;
	// Advisory is only the agent's default for leaving stock and placement
	// unbounded on this node, which an admin can override per provider. A
	// bare-metal pool leaves it false: its counts are exact and an empty pool
	// only fails after payment. An older agent ignores it and bounds.
	Advisory bool `json:"advisory,omitempty"`
}

// NodeUsage is one node's measured utilization. CPUPercent is 0–100 across
// the whole node (all cores); the byte figures are in-use bytes of the same
// totals NodeCapacity reports, so a dimension whose total is 0 (unknown)
// leaves its used figure 0 as well.
type NodeUsage struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemUsedBytes  int64   `json:"mem_used_bytes"`
	DiskUsedBytes int64   `json:"disk_used_bytes"`
}

// CapacityReporter is the optional capacity capability of a provisioner.
// Implement it alongside Provisioner and set SupportsCapacity in Describe;
// the host never calls it without the flag.
type CapacityReporter interface {
	NodeCapacity(ctx context.Context, tenantID string, config map[string]string) (NodeCapacity, error)
}

// PanelRef identifies a provisioned service on panel-capability calls; the
// same shape as ServiceActionInput.
type PanelRef struct {
	TenantID    string
	ServiceID   string
	ExternalRef string
	Config      map[string]string
}

// PanelLogin is a minted single-use login URL into the remote control panel.
// The browser navigates to it directly — a hosting panel is
// customer-reachable by definition, so nothing is bridged and the credential
// embedded in the URL never rests in RackBill.
type PanelLogin struct {
	Status        string // ProvisionSucceeded / ProvisionFailed
	FailureReason string // declared failure, shown to the user
	LoginURL      string
	ExpiresUnix   int64 // validity hint; 0 = unknown
}

// PanelUsageGauge is one current used/limit reading for the subscription — a
// gauge (disk used now, traffic so far this period), deliberately not the
// usage-source Collect shape (integrated consumption for metered billing)
// and not a VM metric sample.
type PanelUsageGauge struct {
	Metric string `json:"metric"` // stable key, e.g. "disk_space", "traffic"
	Label  string `json:"label"`  // human label, shown verbatim
	Used   int64  `json:"used"`
	Limit  int64  `json:"limit"` // 0 = unlimited or unknown
	Unit   string `json:"unit"`  // "bytes" (the only unit in use today)
}

// PanelAccess is the optional panel-access capability of a provisioner.
// Implement it alongside Provisioner and set SupportsPanelLogin and/or
// SupportsPanelUsage in Describe; the host only routes the calls the flags
// declare. userIP is the requesting browser's address, for panels that bind
// an SSO session to the IP that will redeem it.
type PanelAccess interface {
	PanelLoginURL(ctx context.Context, ref PanelRef, userIP string) (PanelLogin, error)
	PanelUsage(ctx context.Context, ref PanelRef) ([]PanelUsageGauge, error)
}

// ConfigOption is one valid value for a config field: a fixed choice of a
// "select" field (ConfigField.Options) or an enumerated one from a field with
// DynamicOptions.
type ConfigOption struct {
	Value string `json:"value"`
	Label string `json:"label"` // shown to the admin; defaults to Value when empty
	// Description explains what choosing this value means — the endpoint it
	// selects, what it needs, what it costs. The panel shows it on hover and
	// under the control while the option is chosen. Optional.
	Description string `json:"description,omitempty"`
}

// HiddenChoiceSuffix marks an enumerated option the remote hides from its
// own customers ("Ubuntu 20.04 (hidden)"). A plugin appends it to the label;
// the agent keeps such a choice from customers on reinstall and at checkout,
// unless it is what runs now or the product's default (decisions 131, 139).
const HiddenChoiceSuffix = " (hidden)"

// ConfigOptionLister is the optional enumeration capability of a provisioner.
// Implement it alongside Provisioner and set DynamicOptions (plus
// OptionsDependOn) on the fields it can enumerate; the host only calls it for
// those fields. config is whatever the admin has entered so far — build the
// client from it exactly as every other call does, and return a clear error
// when it cannot reach or authenticate against the backend: that error is
// shown in the panel and doubles as pre-save credential validation.
type ConfigOptionLister interface {
	ListConfigOptions(ctx context.Context, tenantID, key string, config map[string]string) ([]ConfigOption, error)
}

// BackupCapable is the optional backup capability of a provisioner. Implement
// it alongside Provisioner and set SupportsBackup (and SupportsRestoreBackup
// when RestoreService works) in Describe.
//
// Backups are slow: BackupService should return Pending with a JobRef and let
// the host poll BackupJob rather than holding the RPC open for an hour. The
// host guarantees at-least-once delivery, so a repeated call for a label the
// backend already has must not produce a duplicate snapshot.
type BackupCapable interface {
	BackupService(ctx context.Context, in ServiceBackupInput) (BackupOperation, error)
	RestoreService(ctx context.Context, in ServiceRestoreInput) (BackupOperation, error)
	BackupJob(ctx context.Context, tenantID string, config map[string]string, jobRef string) (BackupOperation, error)
}

// ProvisionerClient adapts a raw gRPC client into the Provisioner interface.
// The returned value also implements VMManager, CapacityReporter, and
// BackupCapable by forwarding; capability gating from Describe flags stays
// the caller's job, exactly as with go-plugin-dialed clients. The host uses
// this for satellite tunnels.
func ProvisionerClient(c proto.ProvisionerClient) Provisioner {
	return &provisionerGRPCClient{client: c}
}

// ProvisionerServer adapts a Provisioner into the wire-facing gRPC server —
// the satellite relay resolves a local plugin per call and delegates here.
func ProvisionerServer(impl Provisioner) proto.ProvisionerServer {
	return &provisionerGRPCServer{impl: impl}
}

// ProvisionerPlugin adapts a Provisioner across the go-plugin gRPC boundary.
type ProvisionerPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl Provisioner
}

func (p *ProvisionerPlugin) GRPCServer(broker *goplugin.GRPCBroker, s *grpc.Server) error {
	proto.RegisterProvisionerServer(s, &provisionerGRPCServer{impl: p.Impl})
	return nil
}

func (p *ProvisionerPlugin) GRPCClient(ctx context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &provisionerGRPCClient{client: proto.NewProvisionerClient(c)}, nil
}

// ServeProvisioner is the entry point for provisioner plugin binaries.
func ServeProvisioner(impl Provisioner) { ServeBundle(Bundle{Provisioner: impl}) }

// --- gRPC adapters ---

type provisionerGRPCServer struct {
	proto.UnimplementedProvisionerServer
	impl Provisioner
}

func (s *provisionerGRPCServer) DescribeProvisioner(ctx context.Context, _ *proto.DescribeProvisionerRequest) (*proto.DescribeProvisionerResponse, error) {
	info, err := s.impl.Describe(ctx)
	if err != nil {
		return nil, err
	}
	fields := configFieldsToProto(info.ConfigFields)
	return &proto.DescribeProvisionerResponse{
		Name: info.Name, DisplayName: info.DisplayName, Version: info.Version, Maturity: info.Maturity,
		ConfigFields: fields, SupportsVm: info.SupportsVM, SupportsConsole: info.SupportsConsole,
		SupportsBackup: info.SupportsBackup, SupportsRestoreBackup: info.SupportsRestoreBackup,
		SupportsCapacity: info.SupportsCapacity, RequiresDomain: info.RequiresDomain,
		SupportsPanelLogin: info.SupportsPanelLogin, SupportsPanelUsage: info.SupportsPanelUsage,
		SupportsRenew: info.SupportsRenew, SupportsGameServer: info.SupportsGameServer,
		NodeDescription: info.NodeDescription, SupportsPasswordReset: info.SupportsPasswordReset,
		SupportsReinstall: info.SupportsReinstall, ReinstallKey: info.ReinstallKey,
		MetricsWindowSeconds: int32(info.MetricsWindowSeconds),
		ReinstallAppKey:      info.ReinstallAppKey, SupportsHostname: info.SupportsHostname,
		SupportsNetwork: info.SupportsNetwork, SupportsReverseDns: info.SupportsReverseDNS,
		SupportsGroups: info.SupportsGroups, SupportsSshKeys: info.SupportsSSHKeys,
		SupportsPrivateNetworks: info.SupportsPrivateNetworks, SupportsProviderBackups: info.SupportsProviderBackups,
		SupportsBootModes: info.SupportsBootModes, SupportsIsoImages: info.SupportsISOImages,
	}, nil
}

func (s *provisionerGRPCServer) Provision(ctx context.Context, req *proto.ProvisionRequest) (*proto.ProvisionResponse, error) {
	res, err := s.impl.Provision(ctx, ProvisionInput{
		TenantID: req.TenantId, ServiceID: req.ServiceId, AccountID: req.AccountId,
		AccountEmail: req.AccountEmail, AccountName: req.AccountName,
		Label: req.Label, Config: req.Config, PeriodEnd: unixTime(req.PeriodEndUnix),
		SSHKeys: sshKeysFromProto(req.SshKeys), AccountSSHKeys: sshKeysFromProto(req.AccountSshKeys),
	})
	if err != nil {
		return nil, err
	}
	return &proto.ProvisionResponse{
		Status: res.Status, ExternalRef: res.ExternalRef,
		FailureReason: res.FailureReason, Details: res.Details,
	}, nil
}

func (s *provisionerGRPCServer) action(ctx context.Context, req *proto.ServiceActionRequest,
	fn func(context.Context, ServiceActionInput) (ServiceActionResult, error)) (*proto.ServiceActionResponse, error) {
	res, err := fn(ctx, ServiceActionInput{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef, Config: req.Config,
	})
	if err != nil {
		return nil, err
	}
	return &proto.ServiceActionResponse{Status: res.Status, FailureReason: res.FailureReason}, nil
}

func (s *provisionerGRPCServer) SuspendService(ctx context.Context, req *proto.ServiceActionRequest) (*proto.ServiceActionResponse, error) {
	return s.action(ctx, req, s.impl.Suspend)
}

func (s *provisionerGRPCServer) UnsuspendService(ctx context.Context, req *proto.ServiceActionRequest) (*proto.ServiceActionResponse, error) {
	return s.action(ctx, req, s.impl.Unsuspend)
}

func (s *provisionerGRPCServer) TerminateService(ctx context.Context, req *proto.ServiceActionRequest) (*proto.ServiceActionResponse, error) {
	return s.action(ctx, req, s.impl.Terminate)
}

// unixTime converts an optional unix timestamp; 0 stays the zero time, which
// is how "the host did not say" reaches an implementation.
func unixTime(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

// unixSeconds is unixTime's inverse: the zero time travels as 0.
func unixSeconds(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func (s *provisionerGRPCServer) RenewService(ctx context.Context, req *proto.RenewServiceRequest) (*proto.ServiceActionResponse, error) {
	m, ok := s.impl.(ServiceRenewer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the renewal capability")
	}
	res, err := m.RenewService(ctx, ServiceRenewInput{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef,
		Config: req.Config, PeriodEnd: unixTime(req.PeriodEndUnix),
	})
	if err != nil {
		return nil, err
	}
	return &proto.ServiceActionResponse{Status: res.Status, FailureReason: res.FailureReason}, nil
}

// vm returns the implementation's optional VM capability, or an
// UNIMPLEMENTED error for plugins that never declared it.
func (s *provisionerGRPCServer) vm() (VMManager, error) {
	if m, ok := s.impl.(VMManager); ok {
		return m, nil
	}
	return nil, status.Error(codes.Unimplemented, "plugin does not implement the VM capability")
}

// backup mirrors vm() for the backup capability.
func (s *provisionerGRPCServer) backup() (BackupCapable, error) {
	if m, ok := s.impl.(BackupCapable); ok {
		return m, nil
	}
	return nil, status.Error(codes.Unimplemented, "plugin does not implement the backup capability")
}

// panel mirrors vm() for the panel-access capability.
func (s *provisionerGRPCServer) panel() (PanelAccess, error) {
	if m, ok := s.impl.(PanelAccess); ok {
		return m, nil
	}
	return nil, status.Error(codes.Unimplemented, "plugin does not implement the panel-access capability")
}

func (s *provisionerGRPCServer) GetPanelLogin(ctx context.Context, req *proto.PanelLoginRequest) (*proto.PanelLoginResponse, error) {
	m, err := s.panel()
	if err != nil {
		return nil, err
	}
	login, err := m.PanelLoginURL(ctx, PanelRef{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef, Config: req.Config,
	}, req.UserIp)
	if err != nil {
		return nil, err
	}
	return &proto.PanelLoginResponse{
		Status: login.Status, FailureReason: login.FailureReason,
		LoginUrl: login.LoginURL, ExpiresUnix: login.ExpiresUnix,
	}, nil
}

func (s *provisionerGRPCServer) GetPanelUsage(ctx context.Context, req *proto.PanelUsageRequest) (*proto.PanelUsageResponse, error) {
	m, err := s.panel()
	if err != nil {
		return nil, err
	}
	gauges, err := m.PanelUsage(ctx, PanelRef{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef, Config: req.Config,
	})
	if err != nil {
		return nil, err
	}
	out := make([]*proto.PanelUsageGauge, len(gauges))
	for i, g := range gauges {
		out[i] = &proto.PanelUsageGauge{
			Metric: g.Metric, Label: g.Label, Used: g.Used, Limit: g.Limit, Unit: g.Unit,
		}
	}
	return &proto.PanelUsageResponse{Gauges: out}, nil
}

func (s *provisionerGRPCServer) GetNodeCapacity(ctx context.Context, req *proto.NodeCapacityRequest) (*proto.NodeCapacityResponse, error) {
	m, ok := s.impl.(CapacityReporter)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the capacity capability")
	}
	cap, err := m.NodeCapacity(ctx, req.TenantId, req.Config)
	if err != nil {
		return nil, err
	}
	resp := &proto.NodeCapacityResponse{
		CpuCores: cap.CPUCores, MemTotalBytes: cap.MemTotalBytes, DiskTotalBytes: cap.DiskTotalBytes,
		ForeignCpuCores: cap.ForeignCPUCores, ForeignMemBytes: cap.ForeignMemBytes,
		ForeignDiskBytes: cap.ForeignDiskBytes, GuestCount: cap.GuestCount, Details: cap.Details,
		CapacityAdvisory: cap.Advisory,
	}
	if u := cap.Usage; u != nil {
		resp.Usage = &proto.NodeUsage{CpuPercent: u.CPUPercent, MemUsedBytes: u.MemUsedBytes, DiskUsedBytes: u.DiskUsedBytes}
	}
	return resp, nil
}

func (s *provisionerGRPCServer) ListConfigOptions(ctx context.Context, req *proto.ListConfigOptionsRequest) (*proto.ListConfigOptionsResponse, error) {
	m, ok := s.impl.(ConfigOptionLister)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement config-option enumeration")
	}
	opts, err := m.ListConfigOptions(ctx, req.TenantId, req.Key, req.Config)
	if err != nil {
		return nil, err
	}
	return &proto.ListConfigOptionsResponse{Options: configOptionsToProto(opts)}, nil
}

func (s *provisionerGRPCServer) BackupService(ctx context.Context, req *proto.BackupServiceRequest) (*proto.BackupOperationResponse, error) {
	m, err := s.backup()
	if err != nil {
		return nil, err
	}
	op, err := m.BackupService(ctx, ServiceBackupInput{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef,
		Config: req.Config, Target: targetFromProto(req.Target), Label: req.Label, Options: req.Options,
	})
	if err != nil {
		return nil, err
	}
	return opToProto(op), nil
}

func (s *provisionerGRPCServer) RestoreService(ctx context.Context, req *proto.RestoreServiceRequest) (*proto.BackupOperationResponse, error) {
	m, err := s.backup()
	if err != nil {
		return nil, err
	}
	op, err := m.RestoreService(ctx, ServiceRestoreInput{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef,
		Config: req.Config, Target: targetFromProto(req.Target), SnapshotID: req.SnapshotId,
		Options: req.Options,
	})
	if err != nil {
		return nil, err
	}
	return opToProto(op), nil
}

func (s *provisionerGRPCServer) GetServiceBackupJob(ctx context.Context, req *proto.BackupJobRequest) (*proto.BackupOperationResponse, error) {
	m, err := s.backup()
	if err != nil {
		return nil, err
	}
	op, err := m.BackupJob(ctx, req.TenantId, req.Config, req.JobRef)
	if err != nil {
		return nil, err
	}
	return opToProto(op), nil
}

func (s *provisionerGRPCServer) GetVMState(ctx context.Context, req *proto.VMRequest) (*proto.VMStateResponse, error) {
	m, err := s.vm()
	if err != nil {
		return nil, err
	}
	st, err := m.VMState(ctx, VMRef{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef, Config: req.Config,
	})
	if err != nil {
		return nil, err
	}
	return &proto.VMStateResponse{State: st.State, UptimeSeconds: st.UptimeSeconds, Details: st.Details}, nil
}

func (s *provisionerGRPCServer) ResetVMPassword(ctx context.Context, req *proto.VMRequest) (*proto.VMPasswordResponse, error) {
	m, ok := s.impl.(VMPasswordResetter)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the password-reset capability")
	}
	password, err := m.VMResetPassword(ctx, VMRef{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef, Config: req.Config,
	})
	if err != nil {
		return nil, err
	}
	return &proto.VMPasswordResponse{Password: password}, nil
}

func (s *provisionerGRPCServer) ReinstallVM(ctx context.Context, req *proto.VMRequest) (*proto.VMPasswordResponse, error) {
	m, ok := s.impl.(VMReinstaller)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the reinstall capability")
	}
	password, err := m.VMReinstall(ctx, VMRef{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef, Config: req.Config,
		SSHKeys: sshKeysFromProto(req.SshKeys),
	})
	if err != nil {
		return nil, err
	}
	return &proto.VMPasswordResponse{Password: password}, nil
}

func vmRefFromProto(req *proto.VMRequest) VMRef {
	if req == nil {
		return VMRef{}
	}
	return VMRef{TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef,
		Config: req.Config, SSHKeys: sshKeysFromProto(req.SshKeys)}
}

func vmRefToProto(ref VMRef) *proto.VMRequest {
	return &proto.VMRequest{TenantId: ref.TenantID, ServiceId: ref.ServiceID, ExternalRef: ref.ExternalRef,
		Config: ref.Config, SshKeys: sshKeysToProto(ref.SSHKeys)}
}

func sshKeysFromProto(in []*proto.SSHKey) []SSHKey {
	if len(in) == 0 {
		return nil
	}
	out := make([]SSHKey, len(in))
	for i, k := range in {
		out[i] = SSHKey{Name: k.Name, PublicKey: k.PublicKey}
	}
	return out
}

func sshKeysToProto(in []SSHKey) []*proto.SSHKey {
	if len(in) == 0 {
		return nil
	}
	out := make([]*proto.SSHKey, len(in))
	for i, k := range in {
		out[i] = &proto.SSHKey{Name: k.Name, PublicKey: k.PublicKey}
	}
	return out
}

func (s *provisionerGRPCServer) SetVMHostname(ctx context.Context, req *proto.VMHostnameRequest) (*proto.VMEmpty, error) {
	m, ok := s.impl.(VMHostnameSetter)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the hostname capability")
	}
	if err := m.VMSetHostname(ctx, vmRefFromProto(req.Vm), req.Hostname); err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.VMEmpty{}, nil
}

func (s *provisionerGRPCServer) GetVMNetwork(ctx context.Context, req *proto.VMRequest) (*proto.VMNetworkResponse, error) {
	m, ok := s.impl.(VMNetworkReader)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the network capability")
	}
	addrs, err := m.VMAddresses(ctx, vmRefFromProto(req))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	out := &proto.VMNetworkResponse{Addresses: make([]*proto.VMAddress, len(addrs))}
	for i, a := range addrs {
		out.Addresses[i] = &proto.VMAddress{Address: a.Address, Range: a.Range, Primary: a.Primary,
			ReverseDns: a.ReverseDNS, ReverseDnsEditable: a.ReverseDNSEditable}
	}
	return out, nil
}

func (s *provisionerGRPCServer) SetVMReverseDNS(ctx context.Context, req *proto.VMReverseDNSRequest) (*proto.VMEmpty, error) {
	m, ok := s.impl.(VMReverseDNSSetter)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the reverse DNS capability")
	}
	if err := m.VMSetReverseDNS(ctx, vmRefFromProto(req.Vm), req.Address, req.Domain); err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.VMEmpty{}, nil
}

func (s *provisionerGRPCServer) GetVMGroups(ctx context.Context, req *proto.VMRequest) (*proto.VMGroupsResponse, error) {
	m, ok := s.impl.(VMGrouper)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the groups capability")
	}
	g, err := m.VMGroups(ctx, vmRefFromProto(req))
	if err != nil {
		return nil, refusedToStatus(err)
	}
	out := &proto.VMGroupsResponse{CurrentId: g.CurrentID, CanCreate: g.CanCreate, Groups: make([]*proto.VMGroup, len(g.Groups))}
	for i, x := range g.Groups {
		out.Groups[i] = &proto.VMGroup{Id: x.ID, Name: x.Name}
	}
	return out, nil
}

func (s *provisionerGRPCServer) SetVMGroup(ctx context.Context, req *proto.VMGroupRequest) (*proto.VMGroup, error) {
	m, ok := s.impl.(VMGrouper)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin does not implement the groups capability")
	}
	g, err := m.VMSetGroup(ctx, vmRefFromProto(req.Vm), req.GroupId, req.NewName)
	if err != nil {
		return nil, refusedToStatus(err)
	}
	return &proto.VMGroup{Id: g.ID, Name: g.Name}, nil
}

func (s *provisionerGRPCServer) VMPowerAction(ctx context.Context, req *proto.VMPowerRequest) (*proto.ServiceActionResponse, error) {
	m, err := s.vm()
	if err != nil {
		return nil, err
	}
	res, err := m.VMPower(ctx, VMRef{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef, Config: req.Config,
	}, req.Action)
	if err != nil {
		return nil, err
	}
	return &proto.ServiceActionResponse{Status: res.Status, FailureReason: res.FailureReason}, nil
}

func (s *provisionerGRPCServer) GetVMMetrics(ctx context.Context, req *proto.VMMetricsRequest) (*proto.VMMetricsResponse, error) {
	m, err := s.vm()
	if err != nil {
		return nil, err
	}
	samples, err := m.VMMetrics(ctx, VMRef{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef, Config: req.Config,
	}, req.Timeframe)
	if err != nil {
		return nil, err
	}
	out := make([]*proto.VMMetricSample, len(samples))
	for i, sm := range samples {
		out[i] = &proto.VMMetricSample{
			UnixSeconds: sm.UnixSeconds, CpuPct: sm.CPUPct,
			MemBytes: sm.MemBytes, MemTotalBytes: sm.MemTotalBytes,
			DiskBytes: sm.DiskBytes, DiskTotalBytes: sm.DiskTotalBytes,
			NetInBps: sm.NetInBps, NetOutBps: sm.NetOutBps,
		}
	}
	return &proto.VMMetricsResponse{Samples: out}, nil
}

func (s *provisionerGRPCServer) GetVMConsole(ctx context.Context, req *proto.VMRequest) (*proto.VMConsoleResponse, error) {
	m, err := s.vm()
	if err != nil {
		return nil, err
	}
	c, err := m.VMConsole(ctx, VMRef{
		TenantID: req.TenantId, ServiceID: req.ServiceId, ExternalRef: req.ExternalRef, Config: req.Config,
	})
	if err != nil {
		return nil, err
	}
	return &proto.VMConsoleResponse{
		Type: c.Type, WebsocketUrl: c.WebsocketURL, Headers: c.Headers,
		Password: c.Password, CaPem: c.CAPEM,
		InsecureSkipVerify: c.InsecureSkipVerify, ExpiresUnix: c.ExpiresUnix,
	}, nil
}

type provisionerGRPCClient struct {
	client proto.ProvisionerClient
}

func (c *provisionerGRPCClient) Describe(ctx context.Context) (ProvisionerInfo, error) {
	resp, err := c.client.DescribeProvisioner(ctx, &proto.DescribeProvisionerRequest{})
	if err != nil {
		return ProvisionerInfo{}, err
	}
	fields := configFieldsFromProto(resp.ConfigFields)
	return ProvisionerInfo{
		Name: resp.Name, DisplayName: resp.DisplayName, Version: resp.Version, Maturity: resp.Maturity,
		ConfigFields: fields, SupportsVM: resp.SupportsVm, SupportsConsole: resp.SupportsConsole,
		SupportsBackup: resp.SupportsBackup, SupportsRestoreBackup: resp.SupportsRestoreBackup,
		SupportsCapacity: resp.SupportsCapacity, RequiresDomain: resp.RequiresDomain,
		SupportsPanelLogin: resp.SupportsPanelLogin, SupportsPanelUsage: resp.SupportsPanelUsage,
		SupportsRenew: resp.SupportsRenew, SupportsGameServer: resp.SupportsGameServer,
		NodeDescription: resp.NodeDescription, SupportsPasswordReset: resp.SupportsPasswordReset,
		SupportsReinstall: resp.SupportsReinstall, ReinstallKey: resp.ReinstallKey,
		MetricsWindowSeconds: int(resp.MetricsWindowSeconds),
		ReinstallAppKey:      resp.ReinstallAppKey, SupportsHostname: resp.SupportsHostname,
		SupportsNetwork: resp.SupportsNetwork, SupportsReverseDNS: resp.SupportsReverseDns,
		SupportsGroups: resp.SupportsGroups, SupportsSSHKeys: resp.SupportsSshKeys,
		SupportsPrivateNetworks: resp.SupportsPrivateNetworks, SupportsProviderBackups: resp.SupportsProviderBackups,
		SupportsBootModes: resp.SupportsBootModes, SupportsISOImages: resp.SupportsIsoImages,
	}, nil
}

func (c *provisionerGRPCClient) Provision(ctx context.Context, in ProvisionInput) (ProvisionResult, error) {
	resp, err := c.client.Provision(ctx, &proto.ProvisionRequest{
		TenantId: in.TenantID, ServiceId: in.ServiceID, AccountId: in.AccountID,
		AccountEmail: in.AccountEmail, AccountName: in.AccountName,
		Label: in.Label, Config: in.Config, PeriodEndUnix: unixSeconds(in.PeriodEnd),
		SshKeys: sshKeysToProto(in.SSHKeys), AccountSshKeys: sshKeysToProto(in.AccountSSHKeys),
	})
	if err != nil {
		return ProvisionResult{}, err
	}
	return ProvisionResult{
		Status: resp.Status, ExternalRef: resp.ExternalRef,
		FailureReason: resp.FailureReason, Details: resp.Details,
	}, nil
}

func (c *provisionerGRPCClient) action(ctx context.Context, in ServiceActionInput,
	fn func(context.Context, *proto.ServiceActionRequest, ...grpc.CallOption) (*proto.ServiceActionResponse, error)) (ServiceActionResult, error) {
	resp, err := fn(ctx, &proto.ServiceActionRequest{
		TenantId: in.TenantID, ServiceId: in.ServiceID, ExternalRef: in.ExternalRef, Config: in.Config,
	})
	if err != nil {
		return ServiceActionResult{}, err
	}
	return ServiceActionResult{Status: resp.Status, FailureReason: resp.FailureReason}, nil
}

func (c *provisionerGRPCClient) Suspend(ctx context.Context, in ServiceActionInput) (ServiceActionResult, error) {
	return c.action(ctx, in, c.client.SuspendService)
}

func (c *provisionerGRPCClient) Unsuspend(ctx context.Context, in ServiceActionInput) (ServiceActionResult, error) {
	return c.action(ctx, in, c.client.UnsuspendService)
}

// RenewService satisfies ServiceRenewer for every gRPC-dialed provisioner;
// plugins that never implemented it answer UNIMPLEMENTED, which is why the
// host gates the call on the Describe flag rather than on a type assertion.
func (c *provisionerGRPCClient) RenewService(ctx context.Context, in ServiceRenewInput) (ServiceActionResult, error) {
	resp, err := c.client.RenewService(ctx, &proto.RenewServiceRequest{
		TenantId: in.TenantID, ServiceId: in.ServiceID, ExternalRef: in.ExternalRef,
		Config: in.Config, PeriodEndUnix: unixSeconds(in.PeriodEnd),
	})
	if err != nil {
		return ServiceActionResult{}, err
	}
	return ServiceActionResult{Status: resp.Status, FailureReason: resp.FailureReason}, nil
}

func (c *provisionerGRPCClient) Terminate(ctx context.Context, in ServiceActionInput) (ServiceActionResult, error) {
	return c.action(ctx, in, c.client.TerminateService)
}

// The client side always exposes VMManager; the host only routes VM calls to
// plugins whose Describe declared the capability.

func (c *provisionerGRPCClient) VMState(ctx context.Context, ref VMRef) (VMState, error) {
	resp, err := c.client.GetVMState(ctx, &proto.VMRequest{
		TenantId: ref.TenantID, ServiceId: ref.ServiceID, ExternalRef: ref.ExternalRef, Config: ref.Config,
	})
	if err != nil {
		return VMState{}, err
	}
	return VMState{State: resp.State, UptimeSeconds: resp.UptimeSeconds, Details: resp.Details}, nil
}

func (c *provisionerGRPCClient) VMPower(ctx context.Context, ref VMRef, action string) (ServiceActionResult, error) {
	resp, err := c.client.VMPowerAction(ctx, &proto.VMPowerRequest{
		TenantId: ref.TenantID, ServiceId: ref.ServiceID, ExternalRef: ref.ExternalRef,
		Config: ref.Config, Action: action,
	})
	if err != nil {
		return ServiceActionResult{}, err
	}
	return ServiceActionResult{Status: resp.Status, FailureReason: resp.FailureReason}, nil
}

func (c *provisionerGRPCClient) VMMetrics(ctx context.Context, ref VMRef, timeframe string) ([]VMMetricSample, error) {
	resp, err := c.client.GetVMMetrics(ctx, &proto.VMMetricsRequest{
		TenantId: ref.TenantID, ServiceId: ref.ServiceID, ExternalRef: ref.ExternalRef,
		Config: ref.Config, Timeframe: timeframe,
	})
	if err != nil {
		return nil, err
	}
	out := make([]VMMetricSample, len(resp.Samples))
	for i, sm := range resp.Samples {
		out[i] = VMMetricSample{
			UnixSeconds: sm.UnixSeconds, CPUPct: sm.CpuPct,
			MemBytes: sm.MemBytes, MemTotalBytes: sm.MemTotalBytes,
			DiskBytes: sm.DiskBytes, DiskTotalBytes: sm.DiskTotalBytes,
			NetInBps: sm.NetInBps, NetOutBps: sm.NetOutBps,
		}
	}
	return out, nil
}

// VMResetPassword is always exposed on the client side; the host only calls it
// for plugins whose Describe declared SupportsPasswordReset.
func (c *provisionerGRPCClient) VMResetPassword(ctx context.Context, ref VMRef) (string, error) {
	resp, err := c.client.ResetVMPassword(ctx, &proto.VMRequest{
		TenantId: ref.TenantID, ServiceId: ref.ServiceID, ExternalRef: ref.ExternalRef, Config: ref.Config,
	})
	if err != nil {
		return "", err
	}
	return resp.Password, nil
}

// VMReinstall is always exposed on the client side; the host only calls it
// for plugins whose Describe declared SupportsReinstall.
func (c *provisionerGRPCClient) VMReinstall(ctx context.Context, ref VMRef) (string, error) {
	resp, err := c.client.ReinstallVM(ctx, &proto.VMRequest{
		TenantId: ref.TenantID, ServiceId: ref.ServiceID, ExternalRef: ref.ExternalRef, Config: ref.Config,
		SshKeys: sshKeysToProto(ref.SSHKeys),
	})
	if err != nil {
		return "", err
	}
	return resp.Password, nil
}

// The host type-asserts the client on each optional capability, so every
// one must keep compiling here.
var (
	_ VMHostnameSetter   = (*provisionerGRPCClient)(nil)
	_ VMNetworkReader    = (*provisionerGRPCClient)(nil)
	_ VMReverseDNSSetter = (*provisionerGRPCClient)(nil)
	_ VMGrouper          = (*provisionerGRPCClient)(nil)
	_ VMReinstaller      = (*provisionerGRPCClient)(nil)
	_ VMPrivateNetworker = (*provisionerGRPCClient)(nil)
	_ VMBackupper        = (*provisionerGRPCClient)(nil)
	_ VMBootModer        = (*provisionerGRPCClient)(nil)
	_ VMISOManager       = (*provisionerGRPCClient)(nil)
)

// The contract 9 VM calls are always exposed on the client side; the host
// only calls each for plugins whose Describe declared its flag. A provider's
// declared refusal comes back as a RefusedError.

func (c *provisionerGRPCClient) VMSetHostname(ctx context.Context, ref VMRef, hostname string) error {
	_, err := c.client.SetVMHostname(ctx, &proto.VMHostnameRequest{Vm: vmRefToProto(ref), Hostname: hostname})
	return refusedFromStatus(err)
}

func (c *provisionerGRPCClient) VMAddresses(ctx context.Context, ref VMRef) ([]VMAddress, error) {
	resp, err := c.client.GetVMNetwork(ctx, vmRefToProto(ref))
	if err != nil {
		return nil, refusedFromStatus(err)
	}
	out := make([]VMAddress, len(resp.Addresses))
	for i, a := range resp.Addresses {
		out[i] = VMAddress{Address: a.Address, Range: a.Range, Primary: a.Primary,
			ReverseDNS: a.ReverseDns, ReverseDNSEditable: a.ReverseDnsEditable}
	}
	return out, nil
}

func (c *provisionerGRPCClient) VMSetReverseDNS(ctx context.Context, ref VMRef, address, domain string) error {
	_, err := c.client.SetVMReverseDNS(ctx, &proto.VMReverseDNSRequest{Vm: vmRefToProto(ref), Address: address, Domain: domain})
	return refusedFromStatus(err)
}

func (c *provisionerGRPCClient) VMGroups(ctx context.Context, ref VMRef) (VMGroups, error) {
	resp, err := c.client.GetVMGroups(ctx, vmRefToProto(ref))
	if err != nil {
		return VMGroups{}, refusedFromStatus(err)
	}
	out := VMGroups{CurrentID: resp.CurrentId, CanCreate: resp.CanCreate, Groups: make([]VMGroup, len(resp.Groups))}
	for i, g := range resp.Groups {
		out.Groups[i] = VMGroup{ID: g.Id, Name: g.Name}
	}
	return out, nil
}

func (c *provisionerGRPCClient) VMSetGroup(ctx context.Context, ref VMRef, groupID, newName string) (VMGroup, error) {
	resp, err := c.client.SetVMGroup(ctx, &proto.VMGroupRequest{Vm: vmRefToProto(ref), GroupId: groupID, NewName: newName})
	if err != nil {
		return VMGroup{}, refusedFromStatus(err)
	}
	return VMGroup{ID: resp.Id, Name: resp.Name}, nil
}

func (c *provisionerGRPCClient) VMConsole(ctx context.Context, ref VMRef) (VMConsole, error) {
	resp, err := c.client.GetVMConsole(ctx, &proto.VMRequest{
		TenantId: ref.TenantID, ServiceId: ref.ServiceID, ExternalRef: ref.ExternalRef, Config: ref.Config,
	})
	if err != nil {
		return VMConsole{}, err
	}
	return VMConsole{
		Type: resp.Type, WebsocketURL: resp.WebsocketUrl, Headers: resp.Headers,
		Password: resp.Password, CAPEM: resp.CaPem,
		InsecureSkipVerify: resp.InsecureSkipVerify, ExpiresUnix: resp.ExpiresUnix,
	}, nil
}

// The client side always exposes CapacityReporter; the host only routes
// capacity calls to plugins whose Describe declared the capability.

func (c *provisionerGRPCClient) NodeCapacity(ctx context.Context, tenantID string, config map[string]string) (NodeCapacity, error) {
	resp, err := c.client.GetNodeCapacity(ctx, &proto.NodeCapacityRequest{TenantId: tenantID, Config: config})
	if err != nil {
		return NodeCapacity{}, err
	}
	out := NodeCapacity{
		CPUCores: resp.CpuCores, MemTotalBytes: resp.MemTotalBytes, DiskTotalBytes: resp.DiskTotalBytes,
		ForeignCPUCores: resp.ForeignCpuCores, ForeignMemBytes: resp.ForeignMemBytes,
		ForeignDiskBytes: resp.ForeignDiskBytes, GuestCount: resp.GuestCount, Details: resp.Details,
		Advisory: resp.CapacityAdvisory,
	}
	if u := resp.Usage; u != nil {
		out.Usage = &NodeUsage{CPUPercent: u.CpuPercent, MemUsedBytes: u.MemUsedBytes, DiskUsedBytes: u.DiskUsedBytes}
	}
	return out, nil
}

// The client side always exposes PanelAccess; the host only routes panel
// calls to plugins whose Describe declared the matching flag.

func (c *provisionerGRPCClient) PanelLoginURL(ctx context.Context, ref PanelRef, userIP string) (PanelLogin, error) {
	resp, err := c.client.GetPanelLogin(ctx, &proto.PanelLoginRequest{
		TenantId: ref.TenantID, ServiceId: ref.ServiceID, ExternalRef: ref.ExternalRef,
		Config: ref.Config, UserIp: userIP,
	})
	if err != nil {
		return PanelLogin{}, err
	}
	return PanelLogin{
		Status: resp.Status, FailureReason: resp.FailureReason,
		LoginURL: resp.LoginUrl, ExpiresUnix: resp.ExpiresUnix,
	}, nil
}

func (c *provisionerGRPCClient) PanelUsage(ctx context.Context, ref PanelRef) ([]PanelUsageGauge, error) {
	resp, err := c.client.GetPanelUsage(ctx, &proto.PanelUsageRequest{
		TenantId: ref.TenantID, ServiceId: ref.ServiceID, ExternalRef: ref.ExternalRef, Config: ref.Config,
	})
	if err != nil {
		return nil, err
	}
	out := make([]PanelUsageGauge, len(resp.Gauges))
	for i, g := range resp.Gauges {
		out[i] = PanelUsageGauge{Metric: g.Metric, Label: g.Label, Used: g.Used, Limit: g.Limit, Unit: g.Unit}
	}
	return out, nil
}

// The client side always exposes ConfigOptionLister; the host only routes
// enumeration calls for fields whose Describe declared dynamic_options.

func (c *provisionerGRPCClient) ListConfigOptions(ctx context.Context, tenantID, key string, config map[string]string) ([]ConfigOption, error) {
	resp, err := c.client.ListConfigOptions(ctx, &proto.ListConfigOptionsRequest{
		TenantId: tenantID, Key: key, Config: config,
	})
	if err != nil {
		return nil, err
	}
	return configOptionsFromProto(resp.Options), nil
}

// The client side always exposes BackupCapable; the host only routes backup
// calls to plugins whose Describe declared the capability.

func (c *provisionerGRPCClient) BackupService(ctx context.Context, in ServiceBackupInput) (BackupOperation, error) {
	resp, err := c.client.BackupService(ctx, &proto.BackupServiceRequest{
		TenantId: in.TenantID, ServiceId: in.ServiceID, ExternalRef: in.ExternalRef,
		Config: in.Config, Target: targetToProto(in.Target), Label: in.Label, Options: in.Options,
	})
	if err != nil {
		return BackupOperation{}, err
	}
	return opFromProto(resp), nil
}

func (c *provisionerGRPCClient) RestoreService(ctx context.Context, in ServiceRestoreInput) (BackupOperation, error) {
	resp, err := c.client.RestoreService(ctx, &proto.RestoreServiceRequest{
		TenantId: in.TenantID, ServiceId: in.ServiceID, ExternalRef: in.ExternalRef,
		Config: in.Config, Target: targetToProto(in.Target), SnapshotId: in.SnapshotID,
		Options: in.Options,
	})
	if err != nil {
		return BackupOperation{}, err
	}
	return opFromProto(resp), nil
}

func (c *provisionerGRPCClient) BackupJob(ctx context.Context, tenantID string, config map[string]string, jobRef string) (BackupOperation, error) {
	resp, err := c.client.GetServiceBackupJob(ctx, &proto.BackupJobRequest{
		TenantId: tenantID, Config: config, JobRef: jobRef,
	})
	if err != nil {
		return BackupOperation{}, err
	}
	return opFromProto(resp), nil
}
