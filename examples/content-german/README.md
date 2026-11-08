# Example German content plugin

This buildable example supplies six German portal strings, including a required
amount placeholder and a plural message. **It is a partial example, not a complete
German translation.** Add translations to [`de.json`](de.json) to cover the entire
portal catalog. No agent source is needed to obtain that catalog.

## Build and install

From the SDK repository root, using the Go version required by `go.mod`:

```sh
go build -o /tmp/content-german ./examples/content-german
/tmp/content-german -rb-plugin-info
```

Use an SDK release supporting contract 16; the minimum compatible agent is
0.2.31. The plugin's own version is independent of both versions. The binary
prefix must be `content-`. Copy it into the operator-managed plugin directory
(`RB_PLUGINS_DIR`, default `plugins-dist/`) and restart the agent. Normal plugin
license/loading rules apply. The inspection flag checks metadata; the host
validates the actual messages when loading the plugin.

In **Admin → Portal content → Content packs**, check the pack's coverage and
apply it to the draft. German can be prepared while disabled; enable it in the
language settings when it should be offered to customers. Existing wording is
preserved by default; choose overwrite only when replacing it intentionally. Review the resulting
copy and use the existing Publish action. Installation and plugin upgrades do
not publish translations automatically. The published text remains available
after disabling or uninstalling the plugin.

## Obtain every available string

An authorized content editor can download the catalog through the Portal content
page or the admin API. For a staff API key, grant `content:read` and ensure its
holder has `content.view` and API access:

```sh
curl --fail --silent --show-error \
  -H "Authorization: Bearer $RACKBILL_API_KEY" \
  https://billing.example.com/api/admin/portal-copy/catalog > catalog.json
```

The response contains `keys` and supported `locales`. Each key includes its
English `defaults.en`, group, text/rich format, declared parameter names/types,
required flags and length limits. Target entries with neither a nonzero
`deprecated_in` nor a `superseded_by` value for a complete pack. Retired keys
remain accepted for compatibility, but do not contribute to current coverage.

Create a translation checklist from the current catalog:

```sh
python3 - <<'PY'
import json
from pathlib import Path

catalog = json.loads(Path("catalog.json").read_text())
template = {
    entry["key"]: entry["defaults"]["en"]
    for entry in catalog["keys"]
    if not entry.get("deprecated_in") and not entry.get("superseded_by")
}
Path("de.template.json").write_text(
    json.dumps(template, ensure_ascii=False, indent=2) + "\n"
)
PY
```

Translate each value, then replace the example's `de.json` with the completed
map. The template initially contains English, so do not distribute it unchanged.
Coverage counts supplied keys; it cannot judge whether prose was translated
correctly. Export a fresh catalog when supporting a newer agent and translate
new keys. A partial pack is valid, and absent entries use RackBill's existing
copy fallback rules. First-party dashboard widget titles, descriptions, empty
states and data labels are part of this same catalog and use the same message
map; newly added dashboard strings need no new plugin format. The Copy tab's
native JSON source export can also be used as a translation file. Keep only
catalog keys when packaging it: tenant-specific composition or announcement
keys are valid tenant edits but do not belong in a reusable plugin.

## Message and pack rules

- Keep key names unchanged. Only catalog keys are accepted; plugins cannot create
  tenant-specific notice, navigation or card keys.
- Preserve parameter names and declared types. For example, the amount in
  `billing.pay.submit` must remain `{amount}`; it is already formatted for the
  reader's locale. Do not hardcode a currency or replace it with `{amount, number}`.
- Keep required placeholders and plural counts. Plurals need an `other` branch:
  `{count, plural, one {# Angebot} other {# Angebote}}`. Use the catalog's message
  grammar and declared parameters rather than arbitrary ICU extensions.
- Respect required labels and byte limits. Rich entries accept RackBill's
  restricted Markdown and validated link targets; they do not accept arbitrary
  HTML, scripts, CSS or template code.
- Choose a supported locale exactly as exported, currently `en`, `de`, `nl`,
  `fr`, `es` or `pt-BR`. A plugin does not add a new locale or RTL support.
- Use stable, unique pack IDs matching `[a-z0-9][a-z0-9-]{0,62}`. Each provider
  supplies 1–32 nonempty packs. Display names are nonblank UTF-8 text up to
  200 bytes; the combined declaration is limited to 2 MiB of content.

An invalid pack rejects the whole plugin load, so correct the named validation
error before installing a replacement. Keep `Describe` deterministic and free
of network calls: the host retains its validated snapshot, and portal requests
do not call the plugin. The same binary can offer several locales or wording
variants through additional `ContentPack` entries, each with its own ID.

To ship content beside another integration, set `Bundle.ContentProvider` in
`sdk.ServeBundle`. The bundle uses the same validation and all-or-nothing loading
rules as other plugin kinds.

Content packs cover portal catalog strings, including document wording. Admin
UI, API errors, provider-specific messages and transactional email templates
have separate behavior and are not translated by this interface.
