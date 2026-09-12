# terraform-provider-hue

**Add lights and devices using the Hue app. This provider manages resources that
are already registered on a Hue bridge.**

A Terraform Plugin Framework provider (protocol v6) for Philips Hue API v2.
Manage rooms, zones, scenes and switch behaviors, and look up lights and devices by UUID.

This is an initial v0 implementation with synthetic fake-bridge tests. Import,
scene updates/deletion, and app-to-Terraform pull have also been exercised on a
physical bridge. It has not been published to the Terraform Registry.
See [the design](docs/design.md) for the specification.

## Build

Go 1.26 or later is required. `mise install` installs the version in `mise.toml`.

```sh
mkdir -p bin
go build -o bin/terraform-provider-hue .
go build -o bin/hue-tf ./cmd/hue-tf
```

For local Terraform development, add a `dev_overrides` entry for
`registry.terraform.io/akr4/hue` pointing to the **absolute** path of your `bin`
directory in your Terraform CLI configuration. Local development overrides do
not require a published provider. Do not run `terraform init` just to discover a
provider supplied through an override.

## Authentication and CLI

Run the following commands yourself against your bridge:

```sh
./bin/hue-tf init
# Press the bridge link button; copy the two printed export commands.
./bin/hue-tf ls light
./bin/hue-tf ls device --json
./bin/hue-tf raw /clip/v2/resource/room
```

`init` discovers bridges using mDNS (`_hue._tcp.local.`), waits up to 60 seconds for
the link button, and creates an application key over HTTPS. If discovery finds
multiple bridges, set `HUE_BRIDGE_HOST` to select one. It also accepts that
variable to bypass discovery. Output goes to stdout as shell-quoted exports;
prompts go to stderr. Protect the printed application key like a password.

`hue-tf ls scene` and `hue-tf ls smart_scene` also show the group type, name, and UUID, sorted by group.
This distinguishes scenes with the same name in different rooms or zones.
`--json` returns the original resource objects for API resource types.
`hue-tf ls switch` joins switch devices with their behavior assignments; its JSON output contains the same summary rows.
See [switch management](docs/switch-management.md) for import and editing.

`hue-tf import-blocks` previews ordinary Terraform import blocks for unmanaged
bridge resources. Add `--write` to create or append to root `imports_hue.tf`.
UUIDs already in state or existing import blocks are skipped. Existing resource
definitions are never updated or deleted, even when the bridge has changed.
Resource definition generation, state imports and infrastructure changes belong
to Terraform.

Use one or more UUIDs to select resources and `--module NAME[.NAME...]` to choose
the import destination. Repeated scene names receive UUID suffixes. Unmatched
selectors block the entire write. See [import block generation](docs/app-to-terraform.md)
for module support and interrupted-run recovery.
The former `pull` command now reports the replacement command; saved pull
baselines are no longer used.

`hue-tf recall SCENE_UUID` applies a saved scene to the lights (or activates a
smart scene). `hue-tf identify DEVICE_UUID_OR_LIGHT_UUID` requests a visual
identification signal. These are explicit runtime writes; they do not alter
Terraform files/state or saved scene definitions. See [runtime commands](docs/runtime-commands.md)
for action modes and device behavior.

Both the provider and CLI use `HUE_BRIDGE_HOST` and
`HUE_BRIDGE_APPLICATION_KEY`. Provider attributes override the environment.
`host` is an IP address or hostname, without a scheme or port.

TLS always validates the bridge's certificate chain against embedded Hue roots.
It intentionally does not match the certificate name against the host. There is
no insecure option, redirect following, HTTP fallback, or environment proxy.
Scene refreshes share a light-capability inventory within a provider configuration.
It contains only gamut and temperature limits, not live light state. Reconfiguring
the provider or writing to the bridge invalidates it; unknown light IDs trigger
a reload. Scene values are always fetched afresh.

Requests are limited to one in flight and five per second. HTTP 429 responses are
retried at most three times, honoring `Retry-After` and context cancellation.
Missing or invalid `Retry-After` uses delays of 1, 2, and 4 seconds. Other failures
are not retried automatically, to avoid duplicate creations.

## Configuration

```hcl
terraform {
  required_providers {
    hue = { source = "akr4/hue" }
  }
}

provider "hue" {} # Credentials from the environment.

data "hue_light" "desk" {
  id = "11111111-1111-4111-8111-111111111111" # Replace with a real light UUID.
}

resource "hue_room" "study" {
  name      = "Study"
  archetype = "office"
  children  = [data.hue_light.desk.device_id]
}

resource "hue_scene" "evening" {
  name  = "Evening"
  group = hue_room.study.id
  actions = {
    (data.hue_light.desk.id) = {
      on         = true
      brightness = 40
      kelvin     = 2700
    }
  }
  speed = 0.6
}
```

Rooms contain **device** UUIDs. Zones contain **light** UUIDs. Children are sets;
scene actions are keyed by light UUID. Group changes replace a scene. Resource
deletion for rooms/zones/scenes removes it from the bridge. Deleting a behavior instance removes its assignment; the paired device remains. Import uses the bridge UUID:

```hcl
import {
  to = hue_room.study
  id = "55555555-5555-4555-8555-555555555555"
}
```

For actions, choose at most one of `mirek` / `kelvin`, and at most one of
`color_xy` / `color_hex`. The other representation is computed. Omitting both
members of a pair removes that property from the action. Comparisons use xy and
mirek, preserving configured values when hardware gamut/range clipping produces
an equivalent result. Actual drift updates both representations in state.
Kelvin conversions are bounded to the API's 153–500 mirek range before writing;
refresh comparison also accounts for each light's narrower supported range. For
gamut `other`, the light's explicit gamut triangle is used when available.
`palette` is a read-only canonical JSON string; `image_id` is preserved on import.
Unchanged scene images are omitted from updates because some app-created scenes
reject image writes. Changing an image can still be rejected by the bridge.

See [provider documentation](docs/index.md), [room](docs/resources/room.md),
[zone](docs/resources/zone.md), and [scene](docs/resources/scene.md).

## Development and testing

Go's built-in testing plus Terraform's official acceptance harness are used.
Only run tests directly related to your change locally, for example:

```sh
go test ./internal/color -run '^TestHexConversion$'
TF_ACC=1 TF_ACC_TERRAFORM_PATH="$(command -v terraform)" \
  go test ./internal/provider -run '^TestAccScene$' -timeout=5m
```

Acceptance tests inject an HTTPS `httptest` bridge; they never use your Hue
environment variables to connect to a real bridge. Fixture data is **synthetic**.
CI runs the full suite with the race detector. Real-device verification is a
separate, user-operated workflow; CI does not connect to a physical bridge.

For repeatable checks against a physical bridge, follow the
[bridge verification guide](docs/bridge-verification.md). It uses a separate local
workspace to inspect existing resources, import them and check for a clean plan.

Generate schema documentation with `make docs`. Generation uses a temporary
output directory and copies only generated pages, preserving the design document.

## Releases

Provider and CLI builds use `.goreleaser.yml` and `.goreleaser-cli.yml`
respectively. The release workflow runs both configurations on a `v*` tag,
creating distinct archives and checksums in a **draft** GitHub release. Provider
checksums are GPG-signed. Configure `GPG_PRIVATE_KEY` and `PASSPHRASE` secrets
before releasing. Publishing the draft and registering the provider in the
Terraform (`akr4/hue`) and OpenTofu registries are manual steps.

## License

MIT. Philips Hue is a trademark of its owner; this project is independent.

Smart scene schedules are supported by `hue_smart_scene`. See [smart scenes](docs/smart-scenes.md) for Hue-specific fields and runtime behavior.

Use `hue-tf show SCENE_UUID` to inspect scene actions, smart scene schedules, and
incoming v2 references. See [scene inspection](docs/show.md) for output and limits.
