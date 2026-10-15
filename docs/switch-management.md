# Manage switches

`hue_behavior_instance` creates, imports, updates and deletes button and dial
assignments. Pair the device in the Hue app first. Once it is paired, you can
create its assignments from `.tf` files.

## Find and import an assignment

Run these commands from a root Terraform directory with the authentication
variables set. If you keep the credentials in 1Password, prefix each command
with `op run --env-file=.env.op --`.

```sh
hue-tf ls switch
hue-tf ls switch --json
hue-tf ls behavior_instance
hue-tf ls behavior_script
```

`ls switch` lists each device that has button or relative_rotary services,
along with the behavior instances whose `configuration.device` refers to it.
It shows the device UUID, name and model, and the behavior UUID and status.
`no v2 assignment` means that API v2 has no such assignment. It does not prove
that no legacy rule or other system controls the device. If the name alone does
not identify the device, check it in the app.

Import the **behavior UUID**, not the device, button or script UUID.

```sh
# Decide between new and managed, and prepare import blocks for new ones
hue-tf import-blocks BEHAVIOR_UUID --module bedroom
hue-tf import-blocks BEHAVIOR_UUID --module bedroom --write
```

A new assignment gets a Terraform resource name taken from the behavior's name.
Managed assignments are skipped. Only import blocks for new assignments are
generated, and existing definitions stay unchanged. If the bridge has no
assignment for the switch at all, follow the steps in the next section.

## Create an assignment for an unconfigured switch

1. Run `hue-tf ls switch` to find the device UUID and whether it already has an
   assignment.
2. Run `hue-tf ls button --json` and use `owner.rid` and `metadata.control_id`
   to find the UUID of each of the device's buttons.
3. Run `hue-tf ls behavior_script --json` to find the script UUID for the model.
4. Define `name`, `enabled`, `script_id` and `configuration` in a
   `hue_behavior_instance`.

Base `configuration` on a device of the same model or on the script's
specification. Use the target device's UUIDs for the device and for each
button. Rooms and scenes can refer to Terraform resource ids. Nothing is filled
in for you, so define every button you need.

`script_id` is required when you create an assignment. You can omit it for an
imported one, and changing a `script_id` you set replaces the resource. For an
important assignment, add `lifecycle { prevent_destroy = true }` to guard
against accidental deletion or replacement.

Check that the plan only creates the assignment you expect, then apply it
yourself. If a behavior that refers to the same device already exists,
creation stops and suggests importing that UUID instead. This check runs just
before creation, so it cannot stop another client from creating one at the
same moment.

The API has no dry run for creation, so a plan cannot confirm that the bridge
accepts the assignment or that the buttons work as intended. After applying,
check `ls switch` and press the buttons.

## Edit an assignment

The main attributes are `name`, `enabled`, `script_id` and
`configuration = jsonencode({...})`.

- `configuration` keeps the whole script-specific structure. Time slots, long
  presses, cycling, dials and unknown extra fields are all written out, not
  picked apart.
- `status` and `last_error` are read-only.
- Creation sends type, script_id, metadata.name, enabled and configuration.
  Updates send only metadata.name, enabled and configuration. Runtime state and
  dependees are never sent.
- `name` is the behavior's name, not the device's.

To change a scene reference, edit the matching `recall` object in
`configuration`:

```hcl
recall = {
  rid   = hue_scene.bedroom_late_night.id
  rtype = "scene"
}
```

Right after generation, UUIDs are literals. Replace the ones for managed rooms
and scenes with resource id references, so that Terraform knows the
dependencies. Define the whole `configuration`, including the other buttons and
settings. Do not copy one model's structure to another model; edit the
configuration you imported instead. Use `enabled` to turn an assignment on or
off.

Check the targets and the diff in the plan, apply it yourself, and press the
switch afterwards. The local tests cover import, update and drift detection
against a fake bridge, so they do not guarantee how a real device behaves.

## Settings changed in the app

`import-blocks` generates import blocks only for unmanaged behaviors. It skips
managed ones and does not update or merge existing `.tf` files. To bring a
change made in the app into your configuration, compare the configuration on
the bridge with your definition and edit the definition.

## Stop managing an assignment

Removing the resource block and applying deletes the assignment from the
bridge, though the device itself stays paired. To keep the assignment and only
stop managing it, replace the resource block with a `removed` block (Terraform
1.7 or later). Inside a room module, `from` is the address relative to that
module.

```hcl
removed {
  from = hue_behavior_instance.switch
  lifecycle {
    destroy = false
  }
}
```

To disable the assignment without removing it, set `enabled = false`.

## API references

- [OpenHue BehaviorInstancePut schema](https://github.com/openhue/openhue-api/blob/main/src/behavior_instance/schemas/BehaviorInstancePut.yaml)
- [OpenHue BehaviorInstanceGet schema](https://github.com/openhue/openhue-api/blob/main/src/behavior_instance/schemas/BehaviorInstanceGet.yaml)
- [node-hue API reference (create/deleteBehaviorInstance)](https://github.com/rodney42/node-hue#api-reference)

The implementation follows the create and delete methods of public API clients,
the fields in the public schemas, and the existing assignments the bridge
returns. The bridge itself validates the full script-specific configuration.
