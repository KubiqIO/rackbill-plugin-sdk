// Command provisioner-example is a commented catalog of provisioner methods.
// Describe works; every provider operation returns an unimplemented error.
package main

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "rackbill.com/sdk/plugin"
)

// The SDK may call one instance concurrently for different tenants. Keep their
// configuration and identifiers on the request, not in shared mutable fields.
type exampleProvisioner struct{}

var _ sdk.Provisioner = exampleProvisioner{}

// Describe supplies identity, configuration fields and supported capabilities.
// Keep it free of provider calls so metadata inspection works without credentials.
func (exampleProvisioner) Describe(context.Context) (sdk.ProvisionerInfo, error) {
	return sdk.ProvisionerInfo{
		Name:            "example",
		DisplayName:     "Example provisioner (scaffold)",
		Version:         "0.1.0", // Your plugin's version, separate from the SDK version.
		Maturity:        sdk.MaturityDev,
		NodeDescription: "One compute node on your provider.",
		ConfigFields: []sdk.ConfigField{
			// Node fields connect each registered node to its provider API.
			{Key: "api_url", Label: "Provider API URL", Type: "text", Scope: sdk.ScopeNode, Required: true, Destination: true},
			{Key: "api_token", Label: "API token", Type: "secret", Scope: sdk.ScopeNode, Required: true},
			// Tenant fields are shared company settings; product fields describe
			// what to create. Calls receive their resolved values in Config.
			{Key: "project", Label: "Provider project", Type: "text", Scope: sdk.ScopeTenant},
			{Key: "plan", Label: "Provider plan", Type: "text", Scope: sdk.ScopeProduct, Required: true},
		},
		// Each optional interface is demonstrated in the other files. Enable
		// only the flags whose provider operations you actually implement.
		SupportsRenew:           false,
		SupportsCapacity:        false,
		SupportsPanelLogin:      false,
		SupportsPanelUsage:      false,
		SupportsBackup:          false,
		SupportsRestoreBackup:   false,
		SupportsVM:              false,
		SupportsConsole:         false,
		SupportsPasswordReset:   false,
		SupportsReinstall:       false,
		SupportsHostname:        false,
		SupportsNetwork:         false,
		SupportsReverseDNS:      false,
		SupportsGroups:          false,
		SupportsSSHKeys:         false,
		SupportsPrivateNetworks: false,
		SupportsProviderBackups: false,
		SupportsBootModes:       false,
		SupportsISOImages:       false,
		SupportsGameServer:      false,
	}, nil
}

// Provision creates or adopts the provider resource for in.ServiceID. Use that
// stable ID for provider idempotency so a retry cannot create a second resource.
// Read settings from in.Config and use ctx for outbound requests. Return
// ProvisionSucceeded with a stable ExternalRef only after creation is confirmed;
// later lifecycle calls receive that reference. Details are shown to customers,
// so keep credentials out. Honor SSHKeys only when SupportsSSHKeys is declared.
func (exampleProvisioner) Provision(ctx context.Context, in sdk.ProvisionInput) (sdk.ProvisionResult, error) {
	return sdk.ProvisionResult{}, unimplemented(ctx, "Provision")
}

// Suspend temporarily restricts an existing resource, for example by disabling
// a hosting account or stopping a VM. Preserve its data and make an already
// suspended resource a successful no-op when implementing this method.
func (exampleProvisioner) Suspend(ctx context.Context, in sdk.ServiceActionInput) (sdk.ServiceActionResult, error) {
	return sdk.ServiceActionResult{}, unimplemented(ctx, "Suspend")
}

// Unsuspend restores the existing resource identified by in.ExternalRef.
// Repeated calls should succeed without creating a replacement resource.
func (exampleProvisioner) Unsuspend(ctx context.Context, in sdk.ServiceActionInput) (sdk.ServiceActionResult, error) {
	return sdk.ServiceActionResult{}, unimplemented(ctx, "Unsuspend")
}

// Terminate permanently removes the provider resource. Confirm ownership using
// the request's tenant and service identifiers, and treat an already-deleted
// resource as success so retries are safe. Never recreate it from this method.
func (exampleProvisioner) Terminate(ctx context.Context, in sdk.ServiceActionInput) (sdk.ServiceActionResult, error) {
	return sdk.ServiceActionResult{}, unimplemented(ctx, "Terminate")
}

// unimplemented keeps every scaffold operation inert and honors cancellation.
// Use SDK logging for diagnostics; stdout belongs to the plugin protocol. Log
// operation names rather than credentials, console tickets or provider payloads.
func unimplemented(ctx context.Context, operation string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sdk.Log.Debug("Example provisioner operation is unimplemented", "operation", operation)
	return status.Error(codes.Unimplemented, operation+" is not implemented by this example")
}

func main() {
	// The SDK provides the handshake, gRPC adapters and -rb-plugin-info flag.
	sdk.ServeProvisioner(exampleProvisioner{})
}
