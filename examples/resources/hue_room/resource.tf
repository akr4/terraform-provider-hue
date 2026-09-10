data "hue_device" "desk" {
  id = "22222222-2222-4222-8222-222222222222"
}

resource "hue_room" "study" {
  name      = "Study"
  archetype = "office"
  children  = [data.hue_device.desk.id]
}
