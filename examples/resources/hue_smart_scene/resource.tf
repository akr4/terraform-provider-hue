resource "hue_smart_scene" "natural" {
  name  = "Natural light"
  group = hue_room.study.id

  transition_duration = 60000
  week_timeslots = [{
    recurrence = ["monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"]
    timeslots = [
      { start_time = "07:00:00", scene = hue_scene.day.id },
      { start_time = "sunset", scene = hue_scene.evening.id },
      { start_time = "00:00:00", scene = hue_scene.dimmed.id },
    ]
  }]
}
