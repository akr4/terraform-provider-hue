package provider

import (
	"context"
	"fmt"

	colors "github.com/akr4/terraform-provider-hue/internal/color"
	"github.com/hashicorp/terraform-plugin-framework/function"
)

type mirekFromKelvinFunction struct{}

func (f *mirekFromKelvinFunction) Metadata(_ context.Context, _ function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "mirek_from_kelvin"
}
func (f *mirekFromKelvinFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary:     "Convert a color temperature in kelvin to mirek",
		Description: "Returns 1,000,000 divided by the color temperature in kelvin, rounded to the nearest integer. The result must be within the API range of 50 to 1000 mirek, about 1000 K to 20000 K. Individual lights may support a narrower range. Requires Terraform 1.8 or later.",
		Parameters:  []function.Parameter{function.Int64Parameter{Name: "kelvin", Description: "Color temperature in kelvin."}},
		Return:      function.Int64Return{},
	}
}
func (f *mirekFromKelvinFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var kelvin int64
	if resp.Error = req.Arguments.Get(ctx, &kelvin); resp.Error != nil {
		return
	}
	mirek := colors.KelvinToMirek(kelvin)
	if kelvin <= 0 || mirek < colors.MinMirek || mirek > colors.MaxMirek {
		resp.Error = function.NewArgumentFuncError(0, fmt.Sprintf("kelvin must convert to %d to %d mirek, about 1000 K to 20000 K; got %d.", colors.MinMirek, colors.MaxMirek, kelvin))
		return
	}
	resp.Error = resp.Result.Set(ctx, mirek)
}
