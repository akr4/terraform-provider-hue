# Rooms usually exist in the Hue app already, so import the room and list the
# devices it contains. A device belongs to one room, so adding a device that is
# in another room moves it here.
import {
  to = hue_room.study
  id = "55555555-5555-4555-8555-555555555555"
}

data "hue_device" "desk" {
  id = "22222222-2222-4222-8222-222222222222"
}

resource "hue_room" "study" {
  name      = "Study"
  archetype = "office"
  children  = [data.hue_device.desk.id]
}
