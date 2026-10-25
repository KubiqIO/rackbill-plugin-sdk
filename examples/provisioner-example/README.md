# Example provisioner plugin

This scaffold shows the required provisioner lifecycle and every optional
provisioner interface in this SDK. Comments beside the methods explain their
inputs, expected results and implementation concerns. Start with the lifecycle,
then keep the optional interfaces your integration needs.

`Describe` returns the example's identity and configuration fields. All
`Supports*` flags are false. Every other operation returns an unimplemented error
or the context's cancellation error; the example creates, changes and deletes
nothing. Implementing a stub is not a reason to enable its capability flag.

## Build and inspect

From the SDK repository root, using the Go version required by `go.mod`:

```sh
go build -o /tmp/provisioner-example ./examples/provisioner-example
/tmp/provisioner-example -rb-plugin-info
go test ./...
```

The inspection command prints the provisioner identity, its plugin version and
the SDK contract number. RackBill manages normal startup through
`sdk.ServeProvisioner`; running the binary without the inspection flag does not
perform a standalone provisioning test. There is no nested `go.mod`, so the SDK's
regular build, vet and test commands include this example.

## Find the methods you need

| File | Coverage |
|---|---|
| [`main.go`](main.go) | Required `Describe`, `Provision`, `Suspend`, `Unsuspend` and `Terminate`, plus startup and the shared unimplemented response |
| [`extensions.go`](extensions.go) | Optional renewal, capacity, panel access, configuration choices and repository backups |
| [`vm.go`](vm.go) | VM state, power, metrics and console; password reset, reinstall, hostname, addresses, reverse DNS and groups |
| [`vm_features.go`](vm_features.go) | Private networks, provider backups, boot modes and customer ISO images |
| [`game.go`](game.go) | The full `GameServerManager` interface: server details, console, commands, files, backups, databases, schedules/tasks, allocations, users, startup variables, settings and activity |

The `var _ sdk.Interface = exampleProvisioner{}` declarations check that each
method group matches its SDK interface. Delete a group's assertion together with
its methods if your plugin does not support it. You may remove entire optional
files; keep `main.go` and the required `sdk.Provisioner` implementation.

Enable capabilities in `Describe` only after implementing and testing the
corresponding methods:

| Interface or behavior | Declaration |
|---|---|
| `ServiceRenewer.RenewService` | `SupportsRenew` |
| `CapacityReporter.NodeCapacity` | `SupportsCapacity`; report capacity for the selected node |
| `PanelAccess.PanelLoginURL` / `PanelUsage` | `SupportsPanelLogin` / `SupportsPanelUsage`, independently |
| `ConfigOptionLister.ListConfigOptions` | `DynamicOptions` on each supported field, with `OptionsDependOn` for its prerequisites |
| `BackupCapable.BackupService`, `RestoreService`, `BackupJob` | `SupportsBackup`; also `SupportsRestoreBackup` when restore works |
| `VMManager` | `SupportsVM`; `SupportsConsole` separately enables `VMConsole` |
| `VMPasswordResetter` / `VMReinstaller` | `SupportsPasswordReset` / `SupportsReinstall`; reinstall also needs `ReinstallKey`, optionally `ReinstallAppKey` |
| `VMHostnameSetter` / `VMNetworkReader` / `VMReverseDNSSetter` / `VMGrouper` | `SupportsHostname` / `SupportsNetwork` / `SupportsReverseDNS` / `SupportsGroups`; reverse DNS also needs network reads |
| `VMPrivateNetworker` / `VMBackupper` | `SupportsPrivateNetworks` / `SupportsProviderBackups` |
| `VMBootModer` / `VMISOManager` | `SupportsBootModes` / `SupportsISOImages`; ISO management also needs boot modes |
| `GameServerManager` | `SupportsGameServer` and `SupportsVM`; `GameServer.Features` lists the supported game features |
| SSH key installation during provisioning/reinstall | `SupportsSSHKeys`; consume the supplied `SSHKeys`, rather than treating all account keys as selected |

VM extensions require a working VM capability. Repository backups
(`BackupCapable`) and backups kept by the provider (`VMBackupper`) are distinct
features. Game-server reads and actions remain gated by the offered feature
list. Keep each enabled interface complete; methods for unadvertised features
may keep their unimplemented responses. `RequiresDomain` is an input requirement:
set it when the service label must be the domain being hosted.

`StorageProvider` and `WidgetProvider` are separate opt-ins shared by plugin
kinds, and are deliberately absent here. Adding their methods opts into those
services without a provisioner `Supports*` flag. See the
[storage interfaces](../../plugin/storage.go) and
[widget guide](https://rackbill.com/docs/plugins/widgets/) when needed.

## Configuration and provider operations

Declare settings in `Describe.ConfigFields` with the scope that owns them:

- `sdk.ScopeProduct` (also the empty default) describes the workload, such as a
  plan, image or resource size.
- `sdk.ScopeTenant` holds an account or hosted-provider connection shared by
  the company's products.
- `sdk.ScopeNode` holds a registered node's endpoint and credentials. Declaring
  a node-scoped field opts the provisioner into node placement; remove those
  fields for a hosted integration that does not use registered nodes.

Each request supplies its effective configuration. Product settings and service
options are overlaid by tenant settings, then by the placed node's settings.
Enumeration receives the configuration entered so far, which may be incomplete.
Use `Type: "secret"` for credentials and `Destination: true` for fields selecting
the endpoint or its TLS trust. Never retain a tenant's credentials or request
context on the shared plugin instance; calls can run concurrently for different
tenants and nodes.

Design lifecycle operations for retries. Use `TenantID` and the stable `ServiceID`
to find or adopt a resource created by an earlier attempt before creating a new
one. Return the provider's durable identifier in `ProvisionResult.ExternalRef`;
later actions receive it. Make suspension, reactivation and termination safe to
repeat. Set a paid-through date from `PeriodEnd` rather than adding another
period on each renewal retry. For repository backups, return a pending operation
and `JobRef` when work continues remotely, then report progress through
`BackupJob`; retries must not create duplicate snapshots.

Pass the supplied context to provider calls. Use `sdk.Log` for diagnostics and
reserve stdout for the SDK protocol. Keep credentials, passwords, console
tokens and sensitive configuration out of logs, errors and returned details.
Report lifecycle success only when the requested state is confirmed.
Use declared failed results for lifecycle refusals and `sdk.Refused(...)` for
supported VM/game actions where the provider's safe explanation should reach
the user; transport failures remain errors. The scaffold's unimplemented
responses are placeholders to replace, not successful empty results.

## Start your own plugin

Copy all source files from the SDK repository root into your own module:

```sh
mkdir ../provisioner-acme
cp examples/provisioner-example/*.go ../provisioner-acme/
cd ../provisioner-acme
go mod init example.com/provisioner-acme
go get kubiq.io/rackbill/sdk@v0.1.0
go mod tidy
```

Choose an available tag from the
[SDK releases](https://github.com/KubiqIO/rackbill-plugin-sdk/releases) that
supports your oldest target agent; `v0.1.0` is the initial-version example.
Only the public SDK and its dependencies are needed.

Rename the implementation, set `Name` to `"acme"`, update `DisplayName`, plugin
`Version` and maturity, and implement the lifecycle. Remove unused optional
groups and enable only capabilities that work. Then build the matching binary:

```sh
go build -o provisioner-acme .
./provisioner-acme -rb-plugin-info
```

Keep the `provisioner-` prefix and match the filename suffix to `Describe.Name`.
Your plugin version, the SDK release version and the SDK contract number are
independent. The SDK supplies the binary's contract requirement automatically;
find its minimum agent version in that SDK Release's compatibility summary or
through `sdk.AgentForContract(sdk.ContractVersion)`.

See the [provisioner guide](https://rackbill.com/docs/plugins/provisioners/) and
the SDK's [provisioner](../../plugin/provisioner.go),
[VM extension](../../plugin/vm_features.go) and
[game-server](../../plugin/gameserver.go) interfaces for full types and contracts.
The [SDK license](../../LICENSE) also applies to this example.
