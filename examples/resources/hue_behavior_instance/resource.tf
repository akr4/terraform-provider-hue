# Import a behavior instance created by the Hue app before applying.
# Configuration varies by script/model. Start with hue-tf pull --new output;
# this example illustrates one button on a generic switch script.
resource "hue_behavior_instance" "switch" {
  name    = "Study switch"
  enabled = true
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
