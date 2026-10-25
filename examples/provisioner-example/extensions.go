package main

import (
	"context"

	sdk "rackbill.com/sdk/plugin"
)

// These interfaces are optional additions to sdk.Provisioner. Keep their
// capability flags or DynamicOptions disabled until their operations work.
var (
	_ sdk.ServiceRenewer     = exampleProvisioner{}
	_ sdk.CapacityReporter   = exampleProvisioner{}
	_ sdk.PanelAccess        = exampleProvisioner{}
	_ sdk.ConfigOptionLister = exampleProvisioner{}
	_ sdk.BackupCapable      = exampleProvisioner{}
)

// RenewService updates a provider-side expiry or paid-through date to
// in.PeriodEnd, for example for a license or subscription. With SupportsRenew
// enabled, apply that absolute date idempotently instead of adding another term
// on each retry. Preserve any later expiry already applied and never reprovision.
func (exampleProvisioner) RenewService(ctx context.Context, in sdk.ServiceRenewInput) (sdk.ServiceActionResult, error) {
	return sdk.ServiceActionResult{}, unimplemented(ctx, "RenewService")
}

// NodeCapacity reports a node's total resources and commitments to resources
// outside RackBill. Exclude RackBill-managed guests from foreign commitments so
// they are not counted twice. Enable SupportsCapacity after this works; populate
// live Usage only from provider measurements, not sums of allocated plans.
func (exampleProvisioner) NodeCapacity(ctx context.Context, tenantID string, config map[string]string) (sdk.NodeCapacity, error) {
	return sdk.NodeCapacity{}, unimplemented(ctx, "NodeCapacity")
}

// PanelLoginURL mints a short-lived, single-use login link for this service's
// customer. Enable SupportsPanelLogin after implementing the provider's SSO
// flow; use userIP if the provider binds the ticket to the browser address.
// Never log the ticket or return standing administrator credentials.
func (exampleProvisioner) PanelLoginURL(ctx context.Context, ref sdk.PanelRef, userIP string) (sdk.PanelLogin, error) {
	return sdk.PanelLogin{}, unimplemented(ctx, "PanelLoginURL")
}

// PanelUsage reads the customer's current used/limit gauges, such as disk and
// traffic, when SupportsPanelUsage is enabled. This is a dashboard read, not a
// billing usage collector. Report provider values in each gauge's stated unit.
func (exampleProvisioner) PanelUsage(ctx context.Context, ref sdk.PanelRef) ([]sdk.PanelUsageGauge, error) {
	return nil, unimplemented(ctx, "PanelUsage")
}

// ListConfigOptions enumerates provider choices such as plans or images using
// the supplied tenant and resolved configuration. Set a field's DynamicOptions
// and OptionsDependOn after implementing its enumeration; no Supports* flag is
// involved. Report provider connection failures instead of fabricated choices.
func (exampleProvisioner) ListConfigOptions(ctx context.Context, tenantID, key string, config map[string]string) ([]sdk.ConfigOption, error) {
	return nil, unimplemented(ctx, "ListConfigOptions")
}

// BackupService exports a snapshot into the repository target in.Target.
// Enable SupportsBackup after implementing this. For a slow export, return a
// pending BackupOperation with a durable JobRef, then let BackupJob poll it.
// Repeated requests for the same service and label must adopt the existing job.
func (exampleProvisioner) BackupService(ctx context.Context, in sdk.ServiceBackupInput) (sdk.BackupOperation, error) {
	return sdk.BackupOperation{}, unimplemented(ctx, "BackupService")
}

// RestoreService restores in.SnapshotID from in.Target into the existing
// service. Enable SupportsRestoreBackup separately from SupportsBackup. Keep
// target credentials confidential and make retries recognize the same restore
// operation; a destructive restore must not be started twice accidentally.
func (exampleProvisioner) RestoreService(ctx context.Context, in sdk.ServiceRestoreInput) (sdk.BackupOperation, error) {
	return sdk.BackupOperation{}, unimplemented(ctx, "RestoreService")
}

// BackupJob reads the durable provider job named by jobRef and returns its
// pending, completed or failed state. Polling should observe the job rather
// than start another backup or restore. Use this call's config and context.
func (exampleProvisioner) BackupJob(ctx context.Context, tenantID string, config map[string]string, jobRef string) (sdk.BackupOperation, error) {
	return sdk.BackupOperation{}, unimplemented(ctx, "BackupJob")
}
