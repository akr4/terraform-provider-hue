# terraform-provider-hue

A Terraform provider for Philips Hue API v2, with a companion CLI, `hue-tf`.
Manage rooms, zones, scenes, [smart scenes](docs/smart-scenes.md), switch
behaviors, and device names and icons.

**Add lights and devices using the Hue app. This provider manages resources that
are already registered on a Hue bridge.**

## Installation

The provider is not yet published to the Terraform Registry. Build it with Go
1.26 or later:

```sh
make build   # bin/terraform-provider-hue and bin/hue-tf
```

Then add a [`dev_overrides`](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers)
entry for `registry.terraform.io/akr4/hue` pointing to the absolute path of `bin`
in your Terraform CLI configuration. `terraform init` is not needed.

## Authentication

```sh
./bin/hue-tf init
# Press the bridge link button, then run the two printed export commands.
```

The provider and CLI read `HUE_BRIDGE_HOST` (IP address or hostname) and
`HUE_BRIDGE_APPLICATION_KEY`. Protect the key like a password.

## Example

```hcl
terraform {
  required_providers {
    hue = { source = "akr4/hue" }
  }
}

provider "hue" {}

data "hue_light" "desk" {
  id = "11111111-1111-4111-8111-111111111111" # A light UUID from `hue-tf ls light`.
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
}
```

Rooms contain **device** UUIDs; zones contain **light** UUIDs; scene actions are
keyed by light UUID. Destroying a resource deletes it from the bridge, except
`hue_device`, which only stops managing the device.

See [resources](docs/resources) and [data sources](docs/data-sources) for the
full reference.

## CLI

| Command | Description |
|---|---|
| `hue-tf ls TYPE [--json]` | List bridge resources such as `light`, `device`, `room`, `scene` and `switch` |
| `hue-tf show SCENE_UUID` | [Inspect a scene](docs/show.md) and what references it |
| `hue-tf raw /clip/v2/PATH` | Print the response of an API v2 GET request |
| `hue-tf import-blocks [--write]` | [Generate `import` blocks](docs/app-to-terraform.md) for unmanaged resources |
| `hue-tf preview` | [Render scene colors](docs/preview.md) from a Terraform plan |
| `hue-tf recall SCENE_UUID` | [Activate a scene](docs/runtime-commands.md) |
| `hue-tf identify UUID` | [Make a device signal](docs/runtime-commands.md) so you can find it |

To import existing resources, run `hue-tf import-blocks --write`, then
`terraform plan -generate-config-out=generated.tf`. For switches, see
[switch management](docs/switch-management.md).

## Development

```sh
TF_ACC=1 go test ./...   # Unit and acceptance tests against a fake bridge
make docs                # Regenerate the reference docs from the schema
```

Tests never connect to a real bridge, even when Hue environment variables are set.

## License

MIT. This is an independent project, not a Signify product.
