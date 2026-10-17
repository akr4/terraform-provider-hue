resource "hue_scene" "evening" {
  name  = "Evening"
  group = hue_zone.desk.id
  actions = {
    (data.hue_light.desk.id) = {
      on         = true
      brightness = 40
      mirek      = provider::hue::mirek_from_kelvin(2700) # 370
    }
  }
}
