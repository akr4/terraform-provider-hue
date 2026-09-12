package hue

import "context"

// ResetLightCapabilities starts a new capability snapshot. Providers call this
// on Configure; writes invalidate it too. Live light state is never cached.
func (c *Client) ResetLightCapabilities() {
	c.capabilitiesMu.Lock()
	defer c.capabilitiesMu.Unlock()
	c.capabilities = nil
}

// LightCapabilities shares one inventory request across scene refreshes on this
// client. It retains only color gamut and temperature limits, never light state.
// A missing ID triggers a fresh inventory so newly paired lights can be found.
func (c *Client) LightCapabilities(ctx context.Context, ids []string) (map[string]Light, error) {
	c.capabilitiesMu.Lock()
	defer c.capabilitiesMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reload := c.capabilities == nil
	for _, id := range ids {
		if _, ok := c.capabilities[id]; !ok {
			reload = true
		}
	}
	if reload {
		var lights []Light
		if err := c.Get(ctx, "/clip/v2/resource/light", &lights); err != nil {
			return nil, err
		}
		next := make(map[string]Light, len(lights))
		for _, light := range lights {
			entry := Light{ID: light.ID}
			if light.Color != nil {
				entry.Color = &Color{GamutType: light.Color.GamutType, Gamut: light.Color.Gamut}
			}
			if light.ColorTemperature != nil {
				entry.ColorTemperature = &ColorTemperature{MirekSchema: light.ColorTemperature.MirekSchema}
			}
			next[light.ID] = entry
		}
		c.capabilities = next
	}
	result := make(map[string]Light, len(ids))
	for _, id := range ids {
		if light, ok := c.capabilities[id]; ok {
			if light.Color != nil {
				color := *light.Color
				if color.Gamut != nil {
					gamut := *color.Gamut
					color.Gamut = &gamut
				}
				light.Color = &color
			}
			if light.ColorTemperature != nil {
				temp := *light.ColorTemperature
				light.ColorTemperature = &temp
			}
			result[id] = light
		}
	}
	return result, nil
}
