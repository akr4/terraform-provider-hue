# Recall and identify

These commands send explicit runtime operations to the Hue Bridge. They do not
read or write Terraform state, edit configuration files, or create resources.
They require the same `HUE_BRIDGE_HOST` and `HUE_BRIDGE_APPLICATION_KEY` as `ls`.

## Recall a scene

```sh
hue-tf recall SCENE_UUID
hue-tf recall SCENE_UUID --action dynamic_palette
hue-tf recall SCENE_UUID --action static
hue-tf recall SMART_SCENE_UUID
hue-tf recall SMART_SCENE_UUID --action deactivate
```

A normal scene defaults to Hue's `active` action: its saved actions are applied
to the lights. A scene configured with `auto_dynamic` may start dynamically.
`dynamic_palette` requests dynamic playback; `static` requests static mode.
A smart scene defaults to `activate`; `deactivate` stops its schedule and is not
an all-lights-off command. Action names follow the Hue API and are checked against
the resource type before any write. No scene definition or schedule is sent.

## Identify a device

```sh
hue-tf identify DEVICE_UUID
hue-tf identify LIGHT_UUID
```

Sends `identify.action = identify` to a device advertising the identify feature.
A light UUID is resolved to its owning device. The visible signal depends on the
hardware (for example, a light's breathe sequence or a sensor's LED). A device
with multiple light services is identified as a device, not as one isolated service.

Both commands accept UUIDs, not Terraform addresses or potentially ambiguous
names. They read a v2 snapshot, verify the target, and send only the requested
runtime field in a PUT. A successful message means the Bridge accepted the
request; it does not verify the visible result. These operations are never run
implicitly by `pull`, `show`, or the Terraform provider.

API references:

- [Scene recall](https://github.com/openhue/openhue-api/blob/main/src/scene/schemas/SceneRecall.yaml)
- [Smart scene recall](https://github.com/openhue/openhue-api/blob/main/src/smart_scene/schemas/SmartSceneRecall.yaml)
- [Device identify implementation in aiohue](https://github.com/home-assistant-libs/aiohue/blob/main/aiohue/v2/controllers/devices.py)
