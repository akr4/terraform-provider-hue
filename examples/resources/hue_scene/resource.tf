data "hue_light" "desk" {
  id = "11111111-1111-4111-8111-111111111111"
}

data "hue_light" "strip" {
  id = "33333333-3333-4333-8333-333333333333"
}

resource "hue_zone" "desk" {
  name     = "Desk"
  children = [data.hue_light.desk.id, data.hue_light.strip.id]
}

resource "hue_scene" "evening" {
  name  = "Evening"
  group = hue_zone.desk.id
  actions = {
    (data.hue_light.desk.id) = {
      on         = true
      brightness = 40
      mirek      = provider::hue::mirek_from_kelvin(2700)
      dynamics   = { duration = 800 }
    }
    (data.hue_light.strip.id) = {
      on         = true
      brightness = 60
      gradient = {
        points = [
          provider::hue::color_from_hex("#ff8800").color_xy,
          provider::hue::color_from_hex("#3355ff").color_xy,
        ]
      }
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

resource "hue_scene" "fireplace" {
  name  = "Fireplace"
  group = hue_zone.desk.id
  actions = {
    (data.hue_light.desk.id) = {
      on         = true
      brightness = 60
      effects_v2 = { effect = "candle", parameters = { speed = 0.4 } }
    }
  }
}
