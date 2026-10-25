package main

import (
	"context"

	sdk "kubiq.io/rackbill/sdk/plugin"
)

// These assertions keep the optional VM method signatures checked against the
// SDK. Describe deliberately advertises none of these unfinished capabilities.
var (
	_ sdk.VMManager          = exampleProvisioner{}
	_ sdk.VMPasswordResetter = exampleProvisioner{}
	_ sdk.VMReinstaller      = exampleProvisioner{}
	_ sdk.VMHostnameSetter   = exampleProvisioner{}
	_ sdk.VMNetworkReader    = exampleProvisioner{}
	_ sdk.VMReverseDNSSetter = exampleProvisioner{}
	_ sdk.VMGrouper          = exampleProvisioner{}
)

// VMState would read the provider's current power state for ref.ExternalRef and
// map it to sdk.VMRunning, VMStopped, VMPaused or VMUnknown. Return uptime only
// when running and keep credentials out of Details. Requires SupportsVM.
func (exampleProvisioner) VMState(ctx context.Context, ref sdk.VMRef) (sdk.VMState, error) {
	return sdk.VMState{}, unimplemented(ctx, "VMState")
}

// VMPower would translate a sdk.VMAction* value into the provider's start,
// graceful shutdown, forced stop or reboot operation. Validate the requested
// action and report provider refusals instead of claiming success. Requires
// SupportsVM; leave retry scheduling to the caller.
func (exampleProvisioner) VMPower(ctx context.Context, ref sdk.VMRef, action string) (sdk.ServiceActionResult, error) {
	return sdk.ServiceActionResult{}, unimplemented(ctx, "VMPower")
}

// VMMetrics would fetch the requested sdk.VMTimeframe* timeframe and normalize
// timestamped samples into VMMetricSample units, including bytes and bytes per
// second. Requires SupportsVM. Set MetricsWindowSeconds in Describe when the
// provider retains less history than the full supported timeframe choices.
func (exampleProvisioner) VMMetrics(ctx context.Context, ref sdk.VMRef, timeframe string) ([]sdk.VMMetricSample, error) {
	return nil, unimplemented(ctx, "VMMetrics")
}

// VMConsole would mint a short-lived console session for this VM. Requires
// SupportsVM and SupportsConsole. Return the provider websocket URL, required
// headers, expiry and any console password; never log session URLs, headers or
// passwords. Preserve TLS verification and provide CAPEM for a private CA.
func (exampleProvisioner) VMConsole(ctx context.Context, ref sdk.VMRef) (sdk.VMConsole, error) {
	return sdk.VMConsole{}, unimplemented(ctx, "VMConsole")
}

// VMResetPassword would ask the provider to set a new guest root password and
// return it for one-time display. Requires SupportsVM and SupportsPasswordReset.
// Never log the returned password or place it in status details or errors.
func (exampleProvisioner) VMResetPassword(ctx context.Context, ref sdk.VMRef) (string, error) {
	return "", unimplemented(ctx, "VMResetPassword")
}

// VMReinstall would destructively reinstall the image in ref.Config[ReinstallKey].
// Requires SupportsVM, SupportsReinstall and a configured ReinstallKey; an
// optional ReinstallAppKey provides an alternative application-template source.
// Honor ref.SSHKeys only when SupportsSSHKeys is advertised and implemented.
// Return any new password for one-time display, never in logs or errors.
func (exampleProvisioner) VMReinstall(ctx context.Context, ref sdk.VMRef) (string, error) {
	return "", unimplemented(ctx, "VMReinstall")
}

// VMSetHostname would set the provider-side name for this VM. Requires
// SupportsVM and SupportsHostname. Apply the provider's own restrictions and use
// sdk.Refused for a rejected name; setting the same hostname again must be safe.
func (exampleProvisioner) VMSetHostname(ctx context.Context, ref sdk.VMRef, hostname string) error {
	return unimplemented(ctx, "VMSetHostname")
}

// VMAddresses would read this VM's assigned addresses and optional CIDR ranges.
// Requires SupportsVM and SupportsNetwork. Mark ReverseDNSEditable only for
// single addresses whose PTR this integration can actually change; a returned
// address block is not an editable per-address reverse DNS record.
func (exampleProvisioner) VMAddresses(ctx context.Context, ref sdk.VMRef) ([]sdk.VMAddress, error) {
	return nil, unimplemented(ctx, "VMAddresses")
}

// VMSetReverseDNS would update one editable address returned by VMAddresses.
// Requires SupportsVM, SupportsNetwork and SupportsReverseDNS. Confirm that the
// address belongs to this VM, honor the provider's rules for clearing a PTR, and
// make setting the same domain again safe.
func (exampleProvisioner) VMSetReverseDNS(ctx context.Context, ref sdk.VMRef, address, domain string) error {
	return unimplemented(ctx, "VMSetReverseDNS")
}

// VMGroups would list provider-side groups belonging to this VM's owner and
// identify the VM's current group. Requires SupportsVM and SupportsGroups. Set
// CanCreate only when VMSetGroup can create a group for that same owner.
func (exampleProvisioner) VMGroups(ctx context.Context, ref sdk.VMRef) (sdk.VMGroups, error) {
	return sdk.VMGroups{}, unimplemented(ctx, "VMGroups")
}

// VMSetGroup would move this VM to groupID or create and select newName, with
// exactly one argument populated. Requires SupportsVM and SupportsGroups. Check
// ownership and use a retry-safe create-or-find operation so repeating a request
// cannot create duplicate groups. Return the group that now holds the VM.
func (exampleProvisioner) VMSetGroup(ctx context.Context, ref sdk.VMRef, groupID, newName string) (sdk.VMGroup, error) {
	return sdk.VMGroup{}, unimplemented(ctx, "VMSetGroup")
}
