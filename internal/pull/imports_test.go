package pull

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPendingImports(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		managed   []ManagedResource
		count     int
		fail      bool
	}{
		{"root", `import {
 to = hue_room.room
 id = "uuid"
}`, nil, 1, false},
		{"module", `import {
 to = module.rooms.module.bedroom.hue_room.room
 id = "uuid"
}`, nil, 1, false},
		{"imported", `import {
 to = hue_room.room
 id = "uuid"
}`, []ManagedResource{{Address: "hue_room.room", ID: "uuid"}}, 0, false},
		{"duplicate ID", `import {
 to = hue_room.other
 id = "uuid"
}`, []ManagedResource{{Address: "hue_room.room", ID: "uuid"}}, 0, true},
		{"different ID", `import {
 to = hue_room.room
 id = "other"
}`, []ManagedResource{{Address: "hue_room.room", ID: "uuid"}}, 0, true},
		{"expression", `import {
 to = hue_room.room
 id = var.id
}`, nil, 0, true},
		{"indexed Hue", `import {
 to = hue_room.room[0]
 id = "uuid"
}`, nil, 1, false},
		{"provider alias", `import {
 to = hue_room.room
 id = "uuid"
 provider = hue.second
}`, nil, 1, false},
		{"for_each", `import {
 for_each = var.ids
 to = hue_room.room[each.key]
 id = each.value
}`, nil, 0, true},
		{"other provider", `import {
 to = aws_instance.example[0]
 id = var.id
}`, nil, 0, false},
		{"device", `import {
 to = hue_device.lamp
 id = "uuid"
}`, nil, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if e := os.WriteFile(filepath.Join(dir, "imports.tf"), []byte(tc.src), 0600); e != nil {
				t.Fatal(e)
			}
			got, e := PendingImports(dir, tc.managed)
			if (e != nil) != tc.fail || (!tc.fail && len(got) != tc.count) {
				t.Fatalf("%+v %v", got, e)
			}
		})
	}
}

func TestPendingImportsReadsJSONAndKeys(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "imports.tf.json"), []byte(`{"import":[{"to":"hue_zone.each[\"day\"]","id":"uuid-a"},{"to":"module.rooms[0].hue_scene.night","id":"uuid-b"}]}`), 0600); e != nil {
		t.Fatal(e)
	}
	// Terraform rejects import blocks in child modules, so they are not read.
	if e := os.MkdirAll(filepath.Join(dir, "child"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "child", "imports.tf"), []byte("import {\n to = hue_room.room\n id = \"uuid-c\"\n}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	got, e := PendingImports(dir, nil)
	if e != nil || len(got) != 2 || got[0].Address != `hue_zone.each["day"]` || got[1].Address != "module.rooms[0].hue_scene.night" {
		t.Fatalf("%+v %v", got, e)
	}
}
