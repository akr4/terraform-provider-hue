package migrate

import (
	"strings"
	"testing"
)

// The configuration that Terraform 1.10 generated for provider 0.1.1, which
// still wrote kelvin.
const generated = `resource "hue_scene" "evening" {
  actions = {
    "11111111-1111-4111-8111-111111111111" = {
      brightness = 25
      color_xy   = null
      dynamics = jsonencode({
        duration = 800
      })
      effects = jsonencode({
        effect = "prism"
      })
      effects_v2 = jsonencode({
        action = {
          effect = "prism"
          parameters = {
            speed = 0.4
          }
        }
      })
      gradient = null
      kelvin   = 2703
      mirek    = 370
      on       = true
    }
    "22222222-2222-4222-8222-222222222222" = {
      brightness = 40
      color_xy   = null
      dynamics   = null
      effects    = null
      effects_v2 = null
      gradient = jsonencode({
        mode = "interpolated_palette"
        points = [{
          color = {
            xy = {
              x = 0.6915
              y = 0.3083
            }
          }
          }, {
          color = {
            xy = {
              x = 0.17
              y = 0.7
            }
          }
        }]
      })
      kelvin = null
      mirek  = null
      on     = true
    }
  }
  auto_dynamic = false
  group        = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
  name         = "Evening"
  palette = jsonencode({
    color = [{
      color = {
        xy = {
          x = 0.3
          y = 0.4
        }
      }
      dimming = {
        brightness = 25
      }
    }]
    color_temperature = []
    dimming           = []
    effects           = []
    effects_v2        = []
  })
  speed = 0.5
}
`

const generatedMigrated = `resource "hue_scene" "evening" {
  actions = {
    "11111111-1111-4111-8111-111111111111" = {
      brightness = 25
      color_xy   = null
      dynamics = {
        duration = 800
      }
      effects = jsonencode({
        effect = "prism"
      })
      effects_v2 = {
        effect = "prism"
        parameters = {
          speed = 0.4
        }
      }
      gradient = null
      mirek    = 370
      on       = true
    }
    "22222222-2222-4222-8222-222222222222" = {
      brightness = 40
      color_xy   = null
      dynamics   = null
      effects    = null
      effects_v2 = null
      gradient = {
        mode = "interpolated_palette"
        points = [{
          x = 0.6915
          y = 0.3083
          }, {
          x = 0.17
          y = 0.7
        }]
      }
      mirek = null
      on    = true
    }
  }
  auto_dynamic = false
  group        = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
  name         = "Evening"
  palette = {
    color = [{
      color_xy = {
        x = 0.3
        y = 0.4
      }
      brightness = 25
    }]
  }
  speed = 0.5
}
`

func migrate(t *testing.T, src string) (string, []Note) {
	t.Helper()
	r, err := File("main.tf", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	// Running migrate again changes nothing.
	again, err := File("main.tf", r.Output)
	if err != nil || string(again.Output) != string(r.Output) {
		t.Fatalf("a second run changed\n%s\ninto\n%s (%v)", r.Output, again.Output, err)
	}
	return string(r.Output), r.Notes
}

func TestGenerated(t *testing.T) {
	got, notes := migrate(t, generated)
	if got != generatedMigrated {
		t.Errorf("got\n%s\nwant\n%s\n%s", got, generatedMigrated, Diff("main.tf", []byte(generatedMigrated), []byte(got)))
	}
	if len(notes) != 0 {
		t.Errorf("notes: %v", notes)
	}
}

func scene(body string) string {
	return "resource \"hue_scene\" \"s\" {\n  name  = \"S\"\n  group = \"g\"\n" + body + "}\n"
}

func TestRewrites(t *testing.T) {
	for name, c := range map[string]struct {
		in, want string
		notes    []string
	}{
		"written palette": {
			scene(`  palette = jsonencode({ color = [{ color = { xy = { x = 0.6, y = 0.3 } }, dimming = { brightness = 40 } }], color_temperature = [{ color_temperature = { mirek = 370 }, dimming = { brightness = 30 } }], dimming = [{ brightness = 20 }] })
  actions = {}
`),
			scene(`  palette = { color = [{ color_xy = { x = 0.6, y = 0.3 }, brightness = 40 }], color_temperature = [{ mirek = 370, brightness = 30 }], dimming = [{ brightness = 20 }] }
  actions = {}
`), nil,
		},
		"palette effects": {
			scene(`  palette = jsonencode({
    effects_v2 = [
      { action = { effect = "sparkle", parameters = { color = { xy = { x = 0.3, y = 0.13 } }, color_temperature = { mirek = 250 }, speed = 0.4 } } },
      { action = { effect = "fire", parameters = {} } },
    ]
  })
  actions = {}
`),
			scene(`  palette = {
    effects_v2 = [
      { effect = "sparkle", parameters = { color_xy = { x = 0.3, y = 0.13 }, mirek = 250, speed = 0.4 } },
      { effect = "fire" },
    ]
  }
  actions = {}
`), nil,
		},
		"palette from a for expression": {
			scene(`  palette = jsonencode({ color = [for c in var.colors : { color = { xy = c.xy }, dimming = { brightness = c.brightness } } if c.on] })
  actions = {}
`),
			scene(`  palette = { color = [for c in var.colors : { color_xy = c.xy, brightness = c.brightness } if c.on] }
  actions = {}
`), nil,
		},
		"quoted keys": {
			scene(`  palette = jsonencode({ "dimming" = [{ "brightness" = 20 }] })
  actions = {}
`),
			scene(`  palette = { dimming = [{ brightness = 20 }] }
  actions = {}
`), nil,
		},
		"empty palette": {
			scene(`  palette = jsonencode({ color = [], color_temperature = [], dimming = [], effects = [], effects_v2 = [] })
  actions = {}
`),
			scene(`  actions = {}
`), []string{"palette had no entries"},
		},
		"deprecated palette effects": {
			scene(`  palette = jsonencode({ effects = [{ effect = "fire" }] })
  actions = {}
`),
			scene(`  palette = jsonencode({ effects = [{ effect = "fire" }] })
  actions = {}
`), []string{"palette was not rewritten: it has entries in the deprecated effects list"},
		},
		"unknown palette field": {
			scene(`  palette = jsonencode({ colour = [] })
  actions = {}
`),
			scene(`  palette = jsonencode({ colour = [] })
  actions = {}
`), []string{"colour is not one of color, color_temperature, dimming, effects, effects_v2"},
		},
		"palette in a heredoc": {
			scene("  palette = <<EOF\n{\"color\": []}\nEOF\n  actions = {}\n"),
			scene("  palette = <<EOF\n{\"color\": []}\nEOF\n  actions = {}\n"),
			[]string{"palette is <<EOF..., which migrate does not read"},
		},
		"palette from a local": {
			scene(`  palette = jsonencode(local.presets.evening.palette)
  actions = {
    "a" = { gradient = jsonencode(merge(local.gradient, { mode = "random_pixelated" })) }
  }
`),
			scene(`  palette = jsonencode(local.presets.evening.palette)
  actions = {
    "a" = { gradient = jsonencode(merge(local.gradient, { mode = "random_pixelated" })) }
  }
`), []string{
				"palette is jsonencode(local.presets.evening.palette), which migrate does not read. Rewrite local.presets.evening.palette in the form that 0.2.0 takes, and write palette = local.presets.evening.palette.",
				"gradient is jsonencode(merge(local.gradient, { mode = \"random_pixelated\" })), which migrate does not read. Write it as an object in the form that 0.2.0 takes.",
			},
		},
		"palette from a variable": {
			scene(`  palette = var.palette
  actions = {}
`),
			scene(`  palette = var.palette
  actions = {}
`), []string{"palette is var.palette, which migrate does not read"},
		},
		"kelvin": {
			scene(`  actions = {
    "a" = { on = true, kelvin = 2700 }
    "b" = { on = true, kelvin = 500 }
    "c" = { kelvin = each.value.kelvin, on = true }
    "d" = { kelvin = null, mirek = 300 }
    "e" = { mirek = null, kelvin = 4000 }
    "f" = { kelvin = 2703, mirek = 370 }
  }
`),
			scene(`  actions = {
    "a" = { on = true, mirek = provider::hue::mirek_from_kelvin(2700) }
    "b" = { on = true, mirek = 1000 }
    "c" = { mirek = provider::hue::mirek_from_kelvin(each.value.kelvin), on = true }
    "d" = { mirek = 300 }
    "e" = { mirek = provider::hue::mirek_from_kelvin(4000) }
    "f" = { mirek = 370 }
  }
`), []string{"kelvin = 500 is outside the range", "kelvin = each.value.kelvin became mirek = provider::hue::mirek_from_kelvin(each.value.kelvin)"},
		},
		"kelvin that is not a whole number": {
			scene(`  actions = {
    "a" = { kelvin = 2700.5 }
  }
`),
			scene(`  actions = {
    "a" = { kelvin = 2700.5 }
  }
`), []string{"kelvin = 2700.5 was not rewritten"},
		},
		"actions in merge and for": {
			scene(`  actions = merge(
    { for id in var.lights : id => merge(provider::hue::color_from_hex("#ff8800"), { on = true, dynamics = jsonencode({ duration = 0 }) }) },
    { "a" = var.on ? { kelvin = 2700 } : { kelvin = 6500 } },
  )
`),
			scene(`  actions = merge(
    { for id in var.lights : id => merge(provider::hue::color_from_hex("#ff8800"), { on = true, dynamics = { duration = 0 } }) },
    { "a" = var.on ? { mirek = provider::hue::mirek_from_kelvin(2700) } : { mirek = provider::hue::mirek_from_kelvin(6500) } },
  )
`), nil,
		},
		"actions from elsewhere": {
			scene(`  actions = { for id, action in var.actions : id => action }
`),
			scene(`  actions = { for id, action in var.actions : id => action }
`), []string{"actions use action, which migrate does not read"},
		},
		"gradient without mode": {
			scene(`  actions = {
    "a" = { gradient = jsonencode({ points = [for p in var.points : { color = { xy = p } }] }) }
  }
`),
			scene(`  actions = {
    "a" = { gradient = { points = [for p in var.points : p] } }
  }
`), nil,
		},
		"effect without action": {
			scene(`  actions = {
    "a" = { effects_v2 = jsonencode({ effect = "fire" }) }
  }
`),
			scene(`  actions = {
    "a" = { effects_v2 = jsonencode({ effect = "fire" }) }
  }
`), []string{"effects_v2 was not rewritten: effect is not one of action"},
		},
		"comments": {
			scene(`  actions = {
    "a" = { effects_v2 = jsonencode({ action = { effect = "fire" } # warm
    }) }
  }
`),
			scene(`  actions = {
    "a" = { effects_v2 = { effect = "fire" } }
  }
`), []string{"Comments inside effects_v2 were not kept."},
		},
		"comment on a removed line": {
			scene(`  actions = {
    "a" = {
      kelvin = null # generated
      mirek  = 370
    }
    "b" = {
      mirek  = 370 # warm
      kelvin = null
    }
  }
`),
			scene(`  actions = {
    "a" = {
      mirek = 370
    }
    "b" = {
      mirek = 370 # warm
    }
  }
`), []string{"A comment on the removed kelvin was not kept."},
		},
		"other resources": {
			`resource "other_light" "l" {
  kelvin  = 2700
  palette = jsonencode({ color = [] })
}
`,
			`resource "other_light" "l" {
  kelvin  = 2700
  palette = jsonencode({ color = [] })
}
`, nil,
		},
		"0.2.0 forms": {
			scene(`  palette = { color = [provider::hue::color_from_hex("#ff8800")] }
  actions = {
    "a" = { mirek = 370, gradient = { points = [{ x = 0.3, y = 0.4 }, { x = 0.4, y = 0.4 }] }, effects_v2 = null, dynamics = { duration = 800 } }
  }
`),
			scene(`  palette = { color = [provider::hue::color_from_hex("#ff8800")] }
  actions = {
    "a" = { mirek = 370, gradient = { points = [{ x = 0.3, y = 0.4 }, { x = 0.4, y = 0.4 }] }, effects_v2 = null, dynamics = { duration = 800 } }
  }
`), nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, notes := migrate(t, c.in)
			if got != c.want {
				t.Errorf("got\n%s\nwant\n%s", got, c.want)
			}
			if len(notes) != len(c.notes) {
				t.Fatalf("notes %v, want %q", notes, c.notes)
			}
			for i, n := range notes {
				if !strings.Contains(n.Text, c.notes[i]) {
					t.Errorf("note %q, want %q", n.Text, c.notes[i])
				}
			}
		})
	}
}

// A file that terraform fmt would change is not formatted, so that only the
// rewritten values change.
func TestUnformatted(t *testing.T) {
	in := "resource \"hue_scene\" \"s\" {\n  name =   \"S\"\n  actions = { \"a\" = { kelvin = 2700 } }\n}\n"
	want := "resource \"hue_scene\" \"s\" {\n  name =   \"S\"\n  actions = { \"a\" = { mirek = provider::hue::mirek_from_kelvin(2700) } }\n}\n"
	if got, _ := migrate(t, in); got != want {
		t.Errorf("got\n%s", got)
	}
}

func TestNotes(t *testing.T) {
	r, err := File("scenes/main.tf", []byte(scene("  palette = jsonencode({})\n  actions = {}\n")))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Notes) != 1 || r.Notes[0].Left || r.Notes[0].String() != "scenes/main.tf:4: hue_scene.s: "+r.Notes[0].Text {
		t.Fatalf("notes %v", r.Notes)
	}
	r, err = File("main.tf", []byte(scene("  palette = var.palette\n  actions = {}\n")))
	if err != nil || len(r.Notes) != 1 || !r.Notes[0].Left {
		t.Fatalf("notes %v, %v", r.Notes, err)
	}
}

func TestModules(t *testing.T) {
	r, err := File("main.tf", []byte(`module "room" {
  source = "./modules/room"
}
module "registry" {
  source = "akr4/room/hue"
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Modules) != 1 || r.Modules[0].Name != "room" || r.Modules[0].Source != "./modules/room" {
		t.Fatalf("modules %+v", r.Modules)
	}
}

func TestParseError(t *testing.T) {
	if _, err := File("main.tf", []byte("resource \"hue_scene\" \"s\" {\n")); err == nil {
		t.Fatal("parsed a broken file")
	}
}

// A file with CRLF line endings keeps them in what migrate writes.
func TestCRLF(t *testing.T) {
	in := strings.ReplaceAll(generated, "\n", "\r\n")
	r, err := File("main.tf", []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(r.Output); got != strings.ReplaceAll(generatedMigrated, "\n", "\r\n") {
		t.Errorf("got %q", got)
	}
}
