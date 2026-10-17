resource "hue_scene" "sunset" {
  name  = "Sunset"
  group = hue_zone.desk.id
  actions = {
    # { color_xy = { x = 0.601, y = 0.3837 }, brightness = 40, on = true }
    (data.hue_light.desk.id) = merge(provider::hue::color_from_hex("#ff8800"), { on = true, brightness = 40 })
  }
}
