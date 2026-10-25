# Example notifier plugin

This is a small, buildable RackBill plugin scaffold. It implements `sdk.Notifier`
with two methods: `Describe` declares the plugin and its configuration form;
`Send` handles one rendered notification. `main` starts it with `sdk.ServeNotifier`.

The example simulates a provider rejection by default. Selecting **Transport
error** returns an error instead. It sends no messages and reports no successful
deliveries. Use it as a starting point for your own provider integration.

## Build and inspect

From the SDK repository root, using the Go version required by `go.mod`:

```sh
go build -o /tmp/notifier-example ./examples/notifier-example
/tmp/notifier-example -rb-plugin-info
go test ./...
```

The inspection command prints JSON containing `kind: "notifier"`, `name:
"example"`, the plugin version and the SDK contract number. Normal startup is
managed by RackBill; running the binary without this flag is not a standalone
notification test.

There is no separate `go.mod` here. Keeping the example inside the SDK module
means the SDK's regular build, vet and test commands compile it too.

## Start your own plugin

From the SDK repository root, copy the implementation into a new module:

```sh
mkdir ../notifier-acme
cp examples/notifier-example/main.go ../notifier-acme/main.go
cd ../notifier-acme
go mod init example.com/notifier-acme
go get rackbill.com/sdk@v0.2.0
go mod tidy
```

Choose an SDK tag from the [published releases](https://github.com/KubiqIO/rackbill-plugin-sdk/releases)
that supports the oldest RackBill agent you target. The command targets SDK
`v0.2.0`; installation requires that release to be available.
The SDK and its public dependencies are sufficient; no RackBill source checkout
is needed.

When adapting `main.go`:

- Change the plugin type, `Name`, `DisplayName` and plugin `Version`. Keep the
  binary's `notifier-` prefix and match its suffix to `Name`.
- Replace `ConfigFields` with the provider's settings. Use `Type: "secret"` for
  credentials and read values from each call's `in.Config`.
- Replace simulated delivery with the provider call. Use the supplied context
  for cancellation; `sdk.HTTPClient` supplies SDK logging for HTTP integrations.
- Return `sdk.SendSent` and the real `ProviderID` after the provider accepts the
  message. Represent a provider rejection with `sdk.SendFailed`; return an error
  when the provider cannot be reached.
- Keep `Describe` free of external calls. Keep the plugin safe for concurrent
  requests from different tenants. Use `sdk.Log` for diagnostics and reserve
  stdout for the SDK protocol.
- Change the declared maturity to reflect your implementation. This example's
  `sdk.MaturityDev` marks its simulated behavior.

After changing `Name` to `"acme"`, build and inspect your plugin:

```sh
go build -o notifier-acme .
./notifier-acme -rb-plugin-info
```

Plugin versions, SDK release versions and SDK contract numbers are independent.
The SDK automatically includes its contract in the binary. Its corresponding
agent requirement is available through
`sdk.AgentForContract(sdk.ContractVersion)` and in that SDK release's notes.

See the [plugin author guide](https://rackbill.com/docs/plugins/) and
[notification provider guide](https://rackbill.com/docs/plugins/notifiers/).
The [SDK license](../../LICENSE) also applies to this example.
