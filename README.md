# RackBill plugin SDK

`kubiq.io/rackbill/sdk` is the standalone Go module for writing RackBill
plugins: payment gateways, provisioners, registrars, DNS providers, usage
sources, fraud screens, notifiers, backup repositories, accounting exports,
login providers, out-of-band consoles, and upstream IPAM providers. It contains no agent code — just the public interfaces,
the proto contract, and the `Serve*` entry points.

```go
import sdk "kubiq.io/rackbill/sdk/plugin"

func main() { sdk.ServeGateway(myGateway{}) }
```

Build the binary as `<kind>-<name>` (e.g. `gateway-acme`) and drop it into
the agent's plugins directory (`RB_PLUGINS_DIR`, default `plugins-dist/`);
the agent discovers, handshakes, and supervises it out-of-process over gRPC
(hashicorp/go-plugin).

Plugins are not limited to Go: any language can implement the gRPC services
directly from the proto files in [`plugin/proto/`](plugin/proto/) (package
`rackbill.plugin.v1`).

## Installing and choosing a version

The SDK is published in
[`KubiqIO/rackbill-plugin-sdk`](https://github.com/KubiqIO/rackbill-plugin-sdk).
Choose a tag from its
[releases](https://github.com/KubiqIO/rackbill-plugin-sdk/releases), read the release
notes for compatibility and migration guidance, and pin that version.

If no releases are listed yet, the first SDK release is still pending. The
following command uses the initial release version as an example:

```sh
go get kubiq.io/rackbill/sdk@v0.1.0
```

SDK versions change when the SDK changes and follow their own release sequence.
An SDK version does not identify a RackBill application or first-party plugin
release. The initial SDK release is `v0.1.0`; during the `v0` series, read the
release notes before upgrading because the API may change.

`sdk.ContractVersion` is a separate compatibility number. It records the plugin
contract required by a compiled plugin; an SDK patch or documentation update need
not change it. Each SDK Release lists its contract version and minimum compatible
RackBill agent version, generated from that release's SDK snapshot.

A plugin built with an SDK carries that SDK's current contract requirement, even
when it implements only older APIs. To support an older agent, pin an SDK release
whose minimum agent requirement supports that installation. Using an older API
with a newer SDK does not lower the plugin's contract requirement.

See [Contract version](#contract-version) below and the
[wire protocol guide](https://rackbill.com/docs/plugins/wire-protocol/).

## Persistent plugin storage

Contract 14 adds host-managed tables for a plugin's own durable state. Implement
`sdk.StorageProvider` on the object passed to a `Serve*` helper:

```go
type driver struct {
    // Other plugin interfaces/methods omitted.
    storage sdk.Storage
}

func (*driver) DescribeStorage(context.Context) (sdk.StorageSchema, error) {
    return sdk.StorageSchema{Version: 1, Tables: []sdk.StorageTable{{
        Name: "resources",
        Columns: []sdk.StorageColumn{
            {Name: "remote_id", Type: "text", Required: true},
            {Name: "attempts", Type: "integer"},
            {Name: "details", Type: "json"},
        },
    }}}, nil
}

func (d *driver) OpenStorage(_ context.Context, storage sdk.Storage) error {
    d.storage = storage // Publish safely if the driver starts other goroutines.
    return nil
}

// Inside a plugin RPC, use the tenant and resource identifiers from its input:
values, err := json.Marshal(map[string]any{"remote_id": externalRef, "attempts": int64(1)})
if err != nil { return err }
err = d.storage.Put(ctx, tenantID, "resources", serviceID, values)
```

`DescribeStorage` must be pure; the host creates/updates tables before calling
`OpenStorage` and before registering the plugin. Retain the storage handle,
not the initialization context. Keep the implementation safe for concurrent
RPCs. Bundles share the primary kind's storage namespace and may explicitly
set `Bundle.Storage` to their storage provider.

`Put` atomically replaces one row by tenant/table/key; omitted optional columns
become null. `Get` returns a JSON object or gRPC `codes.NotFound`. `Delete` is
idempotent. `List(ctx, tenantID, table, after, limit)` returns rows ordered by
key; use the last key as the next page's `after`. Zero selects the default
page size of 25; larger pages are refused. Decode integers into Go `int64` or
use `json.Decoder.UseNumber()` to preserve precision.

Schemas support 1–16 tables with 1–32 columns each. Names match
`[a-z][a-z0-9_]{0,47}`; `tenant_id` and `key` are host-owned columns. Types are
`text`, `integer` (signed int64), `boolean`, and `json`. Keys are 1–256 UTF-8
bytes without NUL. JSON input and stored rows are limited to 64 KiB. Every
operation requires a tenant UUID; always use the tenant from the current RPC.
One plugin process serves all tenants, so the handle is not a tenant-specific
capability. There is no SQL, cross-plugin access, join, or multi-row transaction.

Increase `Version` to add tables or optional columns. Destructive changes,
new required columns on existing tables, same-version changes and downgrades
are refused. Migrations are transactional and serialized across agent replicas.
Data survives process restarts, updates, disable and uninstall. Plugin authors
own retention and deletion of their state. Values are not sealed; credentials
belong in RackBill's configuration system, not these tables.

Operators can set `RB_PLUGIN_DATABASE_URL` to place plugin schemas in a
separate PostgreSQL database. The host alone receives credentials. When unset,
tables live in isolated schemas in the main database. A separate database needs
its own backups; RackBill's built-in backups cover the main database. Changing
the URL does not migrate rows. Storage-dependent plugins cannot currently run
on satellites. Native plugins remain trusted operator-installed binaries; this
API restricts database operations and does not add an OS sandbox.

Other languages implement `StorageManifest` under dispense key `storage` in
[`storage.proto`](plugin/proto/storage.proto), then dial the supplied broker ID
to call the host's reverse `StorageData` service. An agent that cannot satisfy
the schema fails the plugin load before making it available.

## Accounting tax precision

`AccountingInvoiceTax.RateBps` uses `money.TaxRate`, decimal basis points:
`972.5` means `9.725%`. Rates support six decimal places in percent within
0–100%. Parse provider percentages with `money.ParseTaxPercent`, and use
`rate.Apply(amount)` for exact integer tax arithmetic with half-away-from-zero
rounding. Keep money amounts in integer minor units; avoid integer casts or
currency-money parsers when reading a tax rate.

On the wire, `AccountingInvoiceTax.rate_units` contains millionths of a percent
(`9725000` for `9.725%`). Updated SDK servers advertise
`supports_fractional_tax_rates` in `DescribeAccounting`, and SDK clients refuse
fractional exports to older plugins before sending the invoice. Other-language
implementations must advertise the flag and prefer `rate_units` when nonzero,
falling back to legacy integer `rate_bps` for old agents. Whole basis-point
rates retain the legacy field for compatibility.

## Serving several kinds from one binary

Integrations come in sets — a provisioner and the usage source that meters
what it provisions, a notifier and the login provider for the same chat
platform — and an operator installing "the Keyva plugin" should get all of
it from one download. `ServeBundle` serves every implementation you give it
from one process, each kind under its own dispense key:

```go
func main() {
    sdk.ServeBundle(sdk.Bundle{
        Provisioner: keyvaProvisioner{},
        UsageSource: keyvaUsage{},
    })
}
```

Name the binary for its **primary** kind — `provisioner-keyva` — exactly as
for a single-kind plugin: the prefix is what the agent's directory scan
routes on. The binary then declares the rest through the `Bundle` manifest
service (`bundle.proto`, dispense key `bundle`), which the agent probes once
at load and answers by registering each further kind under the machine name
*that* kind's `Describe` returns. Each kind keeps its own name, config
fields, capability flags and maturity; nothing about a kind's contract
changes because it shares a process. A kind the agent does not know is
skipped with a warning, so a bundle built against a newer SDK still loads
for the kinds an older agent has. The single-kind `Serve*` entry points are
one-field bundles, so a plugin that serves one kind gains nothing to do.

Two rules follow from one process holding several registrations. The binary
loads or fails **whole**: a kind whose machine name is already served by
another binary fails the load and names the holder, so a standalone plugin a
bundle now covers is diagnosed rather than half-shadowed — retire the
standalone first. And dashboard widgets are the **union** of what the
implementations offer, routed by widget id, which must therefore be unique
across the binary.

A bundle is one integration, so its account settings are one row. A usage
source served beside a provisioner may declare its account fields with
`Scope: sdk.ScopeTenant`, under the same keys the provisioner declares in
that scope: the agent hides them on the product and reads them from the
provisioner's integration settings at collection, so the operator enters the
credential once, when connecting the provisioner. A usage source with no
provisioner in its binary keeps every field per product.

Implementing the manifest by hand in another language is optional: a binary
that never serves it is a single-kind plugin, exactly as before.

- Author docs: https://rackbill.com/docs/plugins/ — start with the
  [quickstart](https://rackbill.com/docs/plugins/quickstart/).
- Protocol reference: [wire protocol](https://rackbill.com/docs/plugins/wire-protocol/).

## Logging and debugging

Log through `sdk.Log` (a `*slog.Logger`). It writes hclog-shaped JSON to
stderr so the agent files each line under the level you gave it, and lines
from the standard library's `log` package land at info. The agent decides
the level: it sets `RACKBILL_LOG_LEVEL` when it starts your binary and
sends `rackbill-log-level` as gRPC metadata on every call, which the
`Serve*` helpers apply before your handler runs. `sdk.LogLevel()` and
`sdk.DebugEnabled()` read it; you never set it yourself.

**Every call to the system you integrate must be visible at debug: the
URL, the request, and the response.** Make HTTP calls through
`sdk.HTTPClient(timeout)`, or wrap your own transport with
`sdk.LoggingTransport(rt)` when you need a TLS setting or a proxy. Each
exchange becomes one debug record — status, duration, headers, up to 8 KiB
of each body — with authorization headers, secret query parameters and
password/token/key and console-ticket fields masked before it is written
(`sdk.RedactSecrets`). A console endpoint's reply (a last path segment
naming vnc, console, spice or termproxy) is withheld whole. Off debug the transport costs one level check per
call. The agent also records every RPC it makes into your plugin at debug,
so an operator validating your integration sees what they asked and what
your plugin asked in turn.

Stripe Checkout, Stripe PaymentIntents and PayPal Orders withhold both bodies
even at debug, without copying their contents into the log buffer. Their
responses can contain reusable payment references under ordinary `id` fields.
Other gateway authors must likewise keep vault references out of logs, errors,
metadata and hosted checkout results; field-name redaction alone is insufficient.

## Contract version

For gateways, contract 5 adds `RefundInput.IdempotencyKey`, the optional
`RefundChecker.CheckRefund` extension, and `pending` / `requires_action`
refund outcomes. Advertise `SupportsRefundChecks` only when that read is
implemented. Set `IdempotencyWindowSeconds` only when both charge metadata
keys and refund keys prevent repeated money movement across restarts for
at least that duration. Zero leaves uncertain writes for operator review;
it does not prevent ordinary charges or synchronous refunds. Never report
an accepted or pending refund as succeeded. See the
[payment gateway guide](https://rackbill.com/docs/plugins/gateways/).

Contract 11 adds account default payment methods. Set
`SupportsRecurringPayments` only when the adapter supports both hosted vault
enrollment and charging a saved method. An optional
`RecurringPaymentsConfigKey` names the operator checkbox required by a
provider's merchant approval process. `ChargeInput.SavePaymentMethod` requests
vaulting during checkout. When `CheckChargeInput.RetrievePaymentMethod` is
true, independently verify settlement, amount, currency and vault readiness
before returning an opaque `CheckChargeResult.PaymentToken`. A successful
payment without a ready vault reference must leave that token empty.

For subsequent renewals, `ChargeInput.PaymentToken` supplies the confidential
provider reference. Charge the specified amount using the durable idempotency
key, return the provider transaction reference, and let `CheckCharge` confirm
settlement. Preserve a hosted approval URL when the provider requires customer
action. RackBill owns the schedule and protects stored tokens with encryption
bound to the tenant, account and gateway; never create a separate processor
subscription or return a token in `ChargeResult` or user-visible fields.

`sdk.ContractVersion` names the contract between agents and plugins — the
wire, the string vocabularies it carries (config field scopes and types,
capabilities, widget shapes), and what the agent does with them. The SDK
compiles it into your binary and prints it for `-rb-plugin-info`; the
marketplace records it on the catalog entry and an agent refuses to install
or load a plugin built for a contract newer than its own, telling the
operator which agent release the plugin needs. You never set it. If you
change this SDK, `go test ./plugin` fails on any change to the surface until
`ContractVersion` is bumped or the fingerprint is deliberately re-pinned —
see the comment on `ContractVersion`.

Regenerating the proto stubs (from this directory):

```sh
protoc -I . --go_out=paths=source_relative:. --go-grpc_out=paths=source_relative:. plugin/proto/*.proto
```


Registrar plugins declare `RegistrarInfo.MaxRegistrationYears` (contract 13)
to offer prepaid registrations longer than one year. Zero means one year for
older plugins; the agent caps the declaration at ten. Set a limit only if
`RegisterInput.Years` purchases the entire term in one retry-safe operation.
The annual catalog price is charged for every selected year, setup once, and
the full initial term survives queued jobs, retries and pending-operation polls.
Transfers and subsequent renewal billing remain one year.

## License

Use of this SDK is governed by the [RackBill Plugin SDK Commercial License](LICENSE).
