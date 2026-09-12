# Scene preview

`hue-tf preview` reads the JSON output of `terraform show -json` and renders Hue
scene actions. It does not execute Terraform, read `.tf` expressions itself,
contact a Bridge, or change configuration/state. No application key is needed.

```sh
# Terminal: scene changes, before and after
hue-tf preview plan.json

# All scenes in the evaluated configuration
hue-tf preview plan.json --all

# Standalone HTML: all scenes, with search and a changed-scenes filter
hue-tf preview plan.json --html --output preview.html

# JSON on standard input
terraform show -json saved.tfplan | hue-tf preview --html --output preview.html
```

A saved plan is evaluated by Terraform, so variables, references and modules
follow Terraform's semantics. The HTML shows its planned configuration, including
unchanged scenes. It is a snapshot: later `.tf` edits require a new plan.
`terraform show -json` state output is also accepted, but is labeled as a state
snapshot and does not represent unapplied configuration changes. Raw `state pull`
JSON and the streaming `terraform plan -json` format are not accepted.

Terminal colors use 24-bit ANSI on a terminal; redirected output is plain text.
Use `--color always|never|auto` to override detection. `NO_COLOR` disables automatic
color. Terminal swatches show normalized color, with brightness as a separate
number. Unknown plan values and sensitive fields are labeled instead of rendered.

HTML is self-contained and does not load assets or send data over the network.
It shows normalized color and an approximate brightness-adjusted swatch. CSS XYZ
preserves the chromaticity for browser rendering; dimmer percentage scales display
luminance as a preview convention, not a measurement of the lamp's brightness.
Screen gamut limitations apply. Color temperature is approximated; brightness-only
lights use a neutral swatch. Gradients and effects are identified but not simulated.
Smart-scene schedules and other resource types do not currently have visual views.

Names are taken from resources/data sources included in the JSON. When light or
group names are missing, UUIDs are shown. An optional `--inventory resources.json`
can supply names from the response to `hue-tf raw /clip/v2/resource`. This file is
read only for names; it does not replace any planned settings. No API request is
made implicitly. Terraform resource addresses identify definitions; plan JSON
does not reliably expose their original `.tf` filenames.

`--output FILE` writes a complete file before replacing an existing preview.
Terraform sensitive markers are honored. Unrelated values such as provider
configuration, outputs, palettes and behavior JSON are not copied into the HTML.
