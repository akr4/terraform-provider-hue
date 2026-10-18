data "hue_light" "desk" {
  id = "11111111-1111-4111-8111-111111111111"
}

resource "hue_zone" "desk" {
  name     = "Desk"
  children = [data.hue_light.desk.id]
}

resource "hue_scene" "evening" {
  name  = "Evening"
  group = hue_zone.desk.id
  actions = {
    (data.hue_light.desk.id) = {
      on         = true
      brightness = 40
      mirek      = provider::hue::mirek_from_kelvin(2700)
    }
  }
  palette = {
    color = [
      provider::hue::color_from_hex("#ff8800"),
      provider::hue::color_from_hex("#3355ff"),
    ]
  }
  speed        = 0.6
  auto_dynamic = false
}
