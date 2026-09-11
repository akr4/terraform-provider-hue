# Inspect scenes

`hue-tf show SCENE_UUID` reads one v2 resource snapshot and displays an ordinary
scene or smart scene without changing the Bridge, Terraform configuration, or
state. It uses the same `HUE_BRIDGE_HOST` and `HUE_BRIDGE_APPLICATION_KEY`
environment variables as `ls`.

The output includes:

- Resource type, name, UUID, and room or zone.
- Direct incoming typed references from the v2 snapshot, including disabled
  automations. Each entry includes the referencing resource name, UUID, and exact
  JSON path. Paths preserve script-specific button keys and array indexes; they
  do not interpret these as physical button labels or gestures.
- For scenes, action targets with light and room names, on/off, brightness,
  approximate hex color, and color temperature. Gradient and effect presence is
  flagged; the hex swatch does not summarize those features. Brightness is shown
  separately from the xy-to-hex conversion.
- For smart scenes, runtime state, transition duration, and the weekday schedule
  with target scene names and UUIDs.

Missing names are shown as `(unknown)` and UUIDs remain available to distinguish
resources with identical names. Only direct v2 references are inspected. The
command does not inspect v1 rules, manual use, or external clients, and a lack of
references is not proof that deletion is safe.
