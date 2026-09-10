data "hue_light" "desk" {
  id = "11111111-1111-4111-8111-111111111111"
}

resource "hue_room" "study" {
  name     = "Study"
  children = [data.hue_light.desk.device_id]
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
  speed        = 0.6
  auto_dynamic = false
}
