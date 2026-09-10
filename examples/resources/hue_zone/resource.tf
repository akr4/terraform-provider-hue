data "hue_light" "desk" {
  id = "11111111-1111-4111-8111-111111111111"
}

resource "hue_zone" "work" {
  name     = "Work"
  children = [data.hue_light.desk.id]
}
