package hue

import "encoding/json"

type Reference struct {
	RID   string `json:"rid"`
	RType string `json:"rtype"`
}
type Metadata struct {
	Name      string     `json:"name"`
	Archetype string     `json:"archetype,omitempty"`
	Image     *Reference `json:"image,omitempty"`
}
type XY struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type Gamut struct {
	Red   XY `json:"red"`
	Green XY `json:"green"`
	Blue  XY `json:"blue"`
}
type Color struct {
	XY        XY     `json:"xy"`
	Gamut     *Gamut `json:"gamut,omitempty"`
	GamutType string `json:"gamut_type,omitempty"`
}
type MirekSchema struct {
	Min int64 `json:"mirek_minimum"`
	Max int64 `json:"mirek_maximum"`
}
type ColorTemperature struct {
	Mirek       *int64      `json:"mirek"`
	MirekSchema MirekSchema `json:"mirek_schema,omitempty"`
}
type Light struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"`
	Metadata         Metadata          `json:"metadata"`
	Owner            Reference         `json:"owner"`
	Color            *Color            `json:"color,omitempty"`
	ColorTemperature *ColorTemperature `json:"color_temperature,omitempty"`
}
type Device struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Metadata    Metadata `json:"metadata"`
	ProductData struct {
		ModelID string `json:"model_id"`
	} `json:"product_data"`
	Services []Reference `json:"services"`
}
type Group struct {
	ID       string      `json:"id,omitempty"`
	Type     string      `json:"type,omitempty"`
	Metadata Metadata    `json:"metadata"`
	Children []Reference `json:"children"`
}
type On struct {
	On bool `json:"on"`
}
type Dimming struct {
	Brightness float64 `json:"brightness"`
}
type Temperature struct {
	Mirek int64 `json:"mirek"`
}
type ActionColor struct {
	XY XY `json:"xy"`
}
type Action struct {
	Gradient         json.RawMessage `json:"gradient,omitempty"`
	Effects          json.RawMessage `json:"effects,omitempty"`
	EffectsV2        json.RawMessage `json:"effects_v2,omitempty"`
	Dynamics         json.RawMessage `json:"dynamics,omitempty"`
	On               *On             `json:"on,omitempty"`
	Dimming          *Dimming        `json:"dimming,omitempty"`
	Color            *ActionColor    `json:"color,omitempty"`
	ColorTemperature *Temperature    `json:"color_temperature,omitempty"`
}
type SceneAction struct {
	Target Reference `json:"target"`
	Action Action    `json:"action"`
}
type Scene struct {
	ID          string          `json:"id,omitempty"`
	Type        string          `json:"type,omitempty"`
	Metadata    Metadata        `json:"metadata"`
	Group       Reference       `json:"group"`
	Actions     []SceneAction   `json:"actions"`
	Speed       *float64        `json:"speed,omitempty"`
	AutoDynamic *bool           `json:"auto_dynamic,omitempty"`
	Palette     json.RawMessage `json:"palette,omitempty"`
}

// UnmarshalJSON preserves the distinction between an absent/null color
// temperature and a real mirek value when reading scene actions.
func (a *Action) UnmarshalJSON(data []byte) error {
	type plainAction Action
	var decoded plainAction
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var nullable struct {
		Temperature *struct {
			Mirek *int64 `json:"mirek"`
		} `json:"color_temperature"`
	}
	if err := json.Unmarshal(data, &nullable); err != nil {
		return err
	}
	if nullable.Temperature != nil && nullable.Temperature.Mirek == nil {
		decoded.ColorTemperature = nil
	}
	*a = Action(decoded)
	return nil
}
