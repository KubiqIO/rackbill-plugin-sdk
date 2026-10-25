package main

import (
	"context"

	sdk "kubiq.io/rackbill/sdk/plugin"
)

// Each optional interface has its own capability flag. Keeping the methods in
// this scaffold does not make them available while those flags remain false.
var (
	_ sdk.VMPrivateNetworker = exampleProvisioner{}
	_ sdk.VMBackupper        = exampleProvisioner{}
	_ sdk.VMBootModer        = exampleProvisioner{}
	_ sdk.VMISOManager       = exampleProvisioner{}
)

// VMPrivateNetworks would read the owner's networks in this VM's location and
// report attachments. Requires SupportsVM and SupportsPrivateNetworks. Set
// Offered only when the product permits the feature, and CanCreate only when
// network creation is supported and permitted for this owner.
func (exampleProvisioner) VMPrivateNetworks(ctx context.Context, ref sdk.VMRef) (sdk.VMPrivateNetworks, error) {
	return sdk.VMPrivateNetworks{}, unimplemented(ctx, "VMPrivateNetworks")
}

// VMPrivateNetworkAction would translate sdk.PrivateNetwork* actions into
// provider calls. Requires SupportsVM and SupportsPrivateNetworks. Recheck that
// the feature is offered and the network belongs to this owner and location;
// refuse deletion while another VM uses it. Make repeated create/attach/detach
// requests safe using the provider's supported identity and operation controls.
func (exampleProvisioner) VMPrivateNetworkAction(ctx context.Context, ref sdk.VMRef, change sdk.VMPrivateNetworkChange) error {
	return unimplemented(ctx, "VMPrivateNetworkAction")
}

// VMBackups would list backups held by this VM's provider, mapping their status
// and kind to sdk.VMBackup* values. Requires SupportsVM and SupportsProviderBackups,
// which is separate from repository-based SupportsBackup. Report Offered and
// CanSchedule only when the product and provider permit the corresponding work.
func (exampleProvisioner) VMBackups(ctx context.Context, ref sdk.VMRef) (sdk.VMBackups, error) {
	return sdk.VMBackups{}, unimplemented(ctx, "VMBackups")
}

// VMBackupAction would handle sdk.VMBackup* create, restore, delete and schedule
// actions. Requires SupportsVM and SupportsProviderBackups. Validate that the
// feature is offered and any backup belongs to this VM; restore overwrites its
// disks. Use provider operation identities where available to avoid duplicate
// creates, and apply schedule changes as a desired on/off state.
func (exampleProvisioner) VMBackupAction(ctx context.Context, ref sdk.VMRef, action, backupID string) error {
	return unimplemented(ctx, "VMBackupAction")
}

// VMBootMode would read the next boot mode and the ISO images this owner may
// select. Requires SupportsVM and SupportsBootModes. Return sdk.VMBoot* mode
// values and set Offered only when permitted; CanAddISO also requires an
// implemented VMISOManager with SupportsISOImages and room within the ISO limit.
func (exampleProvisioner) VMBootMode(ctx context.Context, ref sdk.VMRef) (sdk.VMBootState, error) {
	return sdk.VMBootState{}, unimplemented(ctx, "VMBootMode")
}

// VMSetBootMode would select disk, rescue or an allowed ISO for the next boot.
// Requires SupportsVM and SupportsBootModes. Recheck that the feature is offered
// and the ISO is available to this VM. Update the desired mode idempotently;
// changing it must not implicitly restart the VM.
func (exampleProvisioner) VMSetBootMode(ctx context.Context, ref sdk.VMRef, mode, isoID string) error {
	return unimplemented(ctx, "VMSetBootMode")
}

// VMISOAction would add an ISO by HTTPS URL for the provider to fetch, or delete
// one owned by this VM's owner. Requires SupportsVM, SupportsBootModes and
// SupportsISOImages. Recheck product permissions and provider limits, validate
// ownership on delete, and never log credentials contained in a source URL.
func (exampleProvisioner) VMISOAction(ctx context.Context, ref sdk.VMRef, change sdk.VMISOChange) error {
	return unimplemented(ctx, "VMISOAction")
}
