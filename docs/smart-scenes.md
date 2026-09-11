# Smart scenes

`hue_smart_scene` manages a room or zone's recurring scene schedule.
It supports creation, import, updates, deletion, and app-to-configuration pull.

- `group` identifies the room or zone. Changing it replaces the smart scene.
- `week_timeslots` contains daily schedules with `recurrence` weekdays and ordered `timeslots`.
- Each timeslot has a `start_time` (`HH:MM:SS` or `sunset`) and a `scene` UUID.
  Direct `hue_scene.NAME.id` references express dependencies.
- `transition_duration` is in milliseconds and defaults to 60000.
- `state` and `image_id` are read-only. Schedule updates preserve the image and never send a recall command.
- An existing scene must belong to the smart scene's room or zone to be used in its schedule.

A sensor or switch can recall the smart scene through its behavior configuration:

```hcl
recall = {
  rid   = hue_smart_scene.natural.id
  rtype = "smart_scene"
}
```

`hue-tf ls smart_scene` includes group names and IDs.
`hue-tf pull` discovers unmanaged smart scenes and prepares import blocks only.
For imported resources, pull updates name, group, schedule, and transition duration while preserving supported references and detecting conflicts.
Runtime activation does not become a configuration edit. Timeslot order is preserved; weekday order is insignificant.

Smart scenes reference ordinary scenes. Include their schedules when checking whether a scene can be deleted, even if the smart scene is not currently active.

API schema: [SmartSceneGet](https://github.com/openhue/openhue-api/blob/main/src/smart_scene/schemas/SmartSceneGet.yaml),
[SmartScenePost](https://github.com/openhue/openhue-api/blob/main/src/smart_scene/schemas/SmartScenePost.yaml),
[SmartScenePut](https://github.com/openhue/openhue-api/blob/main/src/smart_scene/schemas/SmartScenePut.yaml).
