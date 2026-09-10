# terraform-provider-hue

**Add lights and devices using the Hue app. This provider manages resources that
are already registered on a Hue bridge.**

A Terraform Plugin Framework provider (protocol v6) for Philips Hue API v2.
Manage rooms, zones, and scenes, and look up lights and devices by UUID.

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

`hue-tf ls scene` also shows the group type, name, and UUID, sorted by group.
This distinguishes scenes with the same name in different rooms or zones.
`--json` returns the original resource objects.

`hue-tf pull hue_scene.NAME` previews saved scene on/off, brightness, temperature and xy color changes from the app.
Add `--write` to update existing numeric and boolean literals in `.tf`, preserving comments and
references. Switching between temperature (`mirek`/`kelvin`) and `color_xy` is
also supported when the saved scene changes modes. See [the app-to-Terraform workflow](docs/app-to-terraform.md) for scope
and state synchronization.
`hue-tf pull hue_room.NAME` and `hue-tf pull hue_zone.NAME` pull literal names,
archetypes, and children membership.
`hue-tf pull --new` lists unmanaged rooms, zones, and scenes. Use
`hue-tf pull --new RESOURCE_UUID hue_TYPE.NAME [--write]` to preview or create a
new `.tf` definition and print the native Terraform import command.
Addresses may include local modules, such as
`module.bedroom.hue_scene.evening`. Run from the root configuration directory;
pull edits the local module source. Shared module sources and indexed instances
are refused to avoid modifying other instances.

Both the provider and CLI use `HUE_BRIDGE_HOST` and
`HUE_BRIDGE_APPLICATION_KEY`. Provider attributes override the environment.
`host` is an IP address or hostname, without a scheme or port.

TLS always validates the bridge's certificate chain against embedded Hue roots.
It intentionally does not match the certificate name against the host. There is
no insecure option, redirect following, HTTP fallback, or environment proxy.
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
deletion removes it from the bridge. Import uses the bridge UUID:

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
