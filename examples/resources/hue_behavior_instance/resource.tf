# Use the script UUID and device/button UUIDs from your bridge.
# Import first when adopting an existing assignment.
# Configuration varies by script/model. Use hue-tf import-blocks and terraform plan -generate-config-out to import;
# this example illustrates one button on a generic switch script.
resource "hue_behavior_instance" "switch" {
  name      = "Study switch"
  enabled   = true
  script_id = "55555555-5555-4555-8555-555555555555"
  configuration = jsonencode({
    device   = { rid = "22222222-2222-4222-8222-222222222222", rtype = "device" }
    model_id = "RWL022"
    buttons = {
      "44444444-4444-4444-8444-444444444444" = {
        where = [{ group = { rid = hue_room.study.id, rtype = "room" } }]
        on_short_release = {
          recall_single_extended = {
            actions  = [{ action = { recall = { rid = hue_scene.evening.id, rtype = "scene" } } }]
            with_off = { enabled = true }
          }
        }
        on_long_press = { action = "do_nothing" }
      }
    }
  })
}
