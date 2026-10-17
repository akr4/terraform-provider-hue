package provider

import (
	"context"

	colors "github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The attributes that color_from_hex sets in a scene action.
var hexColorTypes = map[string]attr.Type{"color_xy": actionTypes["color_xy"], "brightness": actionTypes["brightness"]}

type colorFromHexFunction struct{}

func (f *colorFromHexFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "color_from_hex"
}
func (f *colorFromHexFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary:     "Convert a hex color to color_xy and brightness",
		Description: "Reads #rrggbb or #rgb with the Philips Wide RGB D65 formula and returns color_xy, rounded to four decimal places, and brightness, the largest RGB channel as a percentage rounded to two decimal places. The formula reaches the full gamut of Hue lights, so a color can look more saturated on a light than on a screen. Black has no color and is an error. In scene actions, merge it with the other settings, function first, so that the settings you write win: merge(provider::hue::color_from_hex(\"#ff8800\"), { on = true, brightness = 40 }). Requires Terraform 1.8 or later.",
		Parameters:  []function.Parameter{function.StringParameter{Name: "hex", Description: "Color as #rrggbb or #rgb."}},
		Return:      function.ObjectReturn{AttributeTypes: hexColorTypes},
	}
}
func (f *colorFromHexFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var hex string
	if resp.Error = req.Arguments.Get(ctx, &hex); resp.Error != nil {
		return
	}
	xy, brightness, err := colors.HexToXYBrightness(hex)
	if err != nil {
		resp.Error = function.NewArgumentFuncError(0, err.Error())
		return
	}
	value, diags := types.ObjectValue(hexColorTypes, map[string]attr.Value{"color_xy": xyValue(xy), "brightness": types.Float64Value(brightness)})
	if diags.HasError() {
		resp.Error = function.FuncErrorFromDiags(ctx, diags)
		return
	}
	resp.Error = resp.Result.Set(ctx, value)
}
