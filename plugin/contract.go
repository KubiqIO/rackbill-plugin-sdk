package plugin

// ContractVersion is the version of the contract between an agent and the
// plugins built against this SDK: the wire (plugin/proto), the vocabularies
// the wire carries as strings — config field scopes and types, maturity
// levels, widget shapes, VM actions — and what the agent does with them. It
// is compiled into every plugin binary, which prints it for -rb-plugin-info
// so the publisher stamps it on the catalog entry, and into every agent,
// which refuses to install, update or load a plugin built for a contract
// newer than its own (plugin architecture §17.4, decision 117). The plugin
// channel publishes on every push to main while agent releases are tagged
// by hand, so without the floor a plugin reaches instances whose agent
// cannot run or present it — which is what the tenant-scoped Keyva did to
// agent 0.1.26.
//
// Bump it when a plugin built against this SDK can need something an older
// agent does not do: a proto field or RPC the host must act on, a new value
// in a vocabulary the host or its panel interprets (a scope, a field type,
// a capability, a shape), a changed meaning. Do not bump it for additions
// only a plugin reads. Every bump adds a row to Contracts and re-pins the
// surface fingerprint in contract_test.go, which is what makes the question
// unmissable: that test fails on any change to the surface until one or the
// other is done deliberately.
const ContractVersion = 15

// Contract is one row of the contract history.
type Contract struct {
	Version int
	// Agent is the first agent release that speaks this contract — what the
	// marketplace shows as the floor ("needs agent 0.1.27 or newer"). The
	// gate itself compares contract numbers, never version strings, so a
	// release numbered differently from what was planned here changes the
	// wording and nothing else.
	Agent string
	// Note says what the version added.
	Note string
}

// Contracts is the history, oldest first; the last row is ContractVersion.
var Contracts = []Contract{
	{Version: 1, Agent: "0.1.0", Note: "the schema-1 plugin channel: every kind's Describe, config fields in the product and node scopes, and the capabilities and vocabularies of the 0.1.x agents"},
	{Version: 2, Agent: "0.1.27", Note: "ConfigField scope \"tenant\": account-level provisioner settings, entered once per company (decision 116)"},
	// The manifest is an RPC the host must act on: an older agent loads a
	// bundle as its primary kind alone and silently drops the rest. Same
	// planned release as contract 2 — both land in the first agent after
	// 0.1.26 — so the floor's wording does not change, only the number.
	{Version: 3, Agent: "0.1.27", Note: "the Bundle manifest: one binary serving several kinds, each registered under its own kind (decision 118)"},
	// An RPC the host must act on: an older agent never asks, so a driver's
	// "this device has no graphical console" would go unheard and the button
	// it meant to withdraw would stay. First release after 0.1.28.
	{Version: 4, Agent: "0.1.29", Note: "DescribeOOBDevice: a driver's per-device console declaration — yes, no, or undeclared — read at save and on Test (decision 120)"},
	{Version: 5, Agent: "0.1.30", Note: "durable payment and refund attempts, bounded idempotent retries, and optional CheckRefund with pending and requires_action outcomes"},
	{Version: 6, Agent: "0.1.31", Note: "explicit currency units and provider-observed charge amount/currency verification before settlement"},
	// A field the host must act on: an older agent would store a Sensitive
	// value (cloud-init that may hold credentials) in plain text, which is the
	// failure the flag exists to prevent (decision 124).
	{Version: 7, Agent: "0.1.34", Note: "ConfigField.Sensitive: workload values sealed at rest, withheld on read and kept off invoices, views and logs; ResetVMPassword (supports_password_reset), a root password shown once and never stored"},
	// RPCs and fields the host must act on: an older agent never offers the
	// reinstall, and would bound placement on a capacity a newer plugin marks
	// advisory (decision 129). First release after 0.2.4.
	{Version: 8, Agent: "0.2.5", Note: "ReinstallVM (supports_reinstall, reinstall_key): wipe and reinstall a VM from the service's image option, any password shown once; NodeCapacityResponse.capacity_advisory: reported capacity that does not bound placement (decisions 129-130)"},
	// RPCs and fields the host must act on: an older agent never offers the
	// rename, rDNS, groups or saved keys, and would install an image over an
	// application pick it cannot clear. First release after 0.2.5.
	{Version: 9, Agent: "0.2.6", Note: "SetVMHostname, GetVMNetwork, SetVMReverseDNS, GetVMGroups/SetVMGroup (supports_hostname, supports_network, supports_reverse_dns, supports_groups); ssh_keys on Provision and ReinstallVM (supports_ssh_keys); reinstall_app_key, a second reinstall option; HiddenChoiceSuffix (decisions 131-136)"},
	// RPCs and fields the host must act on: an older agent never offers the
	// private networks, provider backups, boot modes or ISOs, so a plugin
	// declaring them would show nothing. First release after 0.2.7.
	{Version: 10, Agent: "0.2.8", Note: "GetVMPrivateNetworks/VMPrivateNetworkAction, GetVMBackups/VMBackupAction, GetVMBootMode/SetVMBootMode, VMISOAction (supports_private_networks, supports_provider_backups, supports_boot_modes, supports_iso_images), each read carrying offered (decisions 140-143)"},
	{Version: 11, Agent: "0.2.11", Note: "Recurring payments: explicit hosted vault consent, confidential provider payment tokens, off-session Charge and verified token retrieval through CheckCharge"},
	// RPCs the host must act on: an older agent never offers zone hosting,
	// so a registrar declaring it would show nothing. Same planned release
	// as contract 11, so the floor's wording does not change.
	{Version: 12, Agent: "0.2.11", Note: "registrar DNS zone hosting: ListZoneRecords, PutZone, DeleteZone (supports_dns_hosting), the agent owning the record set (decision 146)"},
	{Version: 13, Agent: "0.2.20", Note: "registrar max_registration_years: cap prepaid registration terms before checkout (decision 147)"},
	{Version: 14, Agent: "0.2.21", Note: "host-mediated plugin storage: declarative schemas, additive migrations and namespace-bound tenant-scoped CRUD over a reverse gRPC service"},
	// A scope and an RPC the host must act on: an older agent would show a
	// product-scoped accounting field on the connection form and require it,
	// and never ask for its options. Same planned release as contract 14.
	{Version: 15, Agent: "0.2.21", Note: "accounting product-scoped config fields with ListConfigOptions, per-line product mapping on SyncInvoice, and refund allocation lines on SyncRefund (decisions 149-151)"},
}

// AgentForContract returns the first agent release that speaks the
// contract, or "" for one this SDK does not know — a plugin from a newer
// SDK than the agent reading its catalog entry.
func AgentForContract(v int) string {
	for _, c := range Contracts {
		if c.Version == v {
			return c.Agent
		}
	}
	return ""
}
