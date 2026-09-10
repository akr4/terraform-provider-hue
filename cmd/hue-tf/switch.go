package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/akr4/terraform-provider-hue/internal/hue"
)

func listSwitches(ctx context.Context, client *hue.Client, out io.Writer, asJSON bool) error {
	var devices []hue.Device
	if err := client.Get(ctx, "/clip/v2/resource/device", &devices); err != nil {
		return err
	}
	var behaviors []hue.BehaviorInstance
	if err := client.Get(ctx, "/clip/v2/resource/behavior_instance", &behaviors); err != nil {
		return err
	}
	type row struct {
		DeviceID   string `json:"device_id"`
		Name       string `json:"name"`
		Model      string `json:"model"`
		BehaviorID string `json:"behavior_id"`
		Status     string `json:"status"`
	}
	rows := []row{}
	for _, d := range devices {
		switchDevice := false
		for _, s := range d.Services {
			if s.RType == "button" || s.RType == "relative_rotary" {
				switchDevice = true
			}
		}
		if !switchDevice {
			continue
		}
		found := false
		for _, b := range behaviors {
			var config struct {
				Device hue.Reference `json:"device"`
			}
			if err := json.Unmarshal(b.Configuration, &config); err != nil {
				return err
			}
			if config.Device.RID == d.ID && config.Device.RType == "device" {
				rows = append(rows, row{d.ID, d.Metadata.Name, d.ProductData.ModelID, b.ID, b.Status})
				found = true
			}
		}
		if !found {
			rows = append(rows, row{d.ID, d.Metadata.Name, d.ProductData.ModelID, "", "no v2 assignment"})
		}
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "DEVICE ID\tNAME\tMODEL\tBEHAVIOR ID\tSTATUS")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", tableText(r.DeviceID), tableText(r.Name), tableText(r.Model), tableText(r.BehaviorID), tableText(r.Status))
	}
	return w.Flush()
}
