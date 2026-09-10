package main

import (
	"context"
	"flag"
	"log"

	"github.com/akr4/terraform-provider-hue/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

var version = "dev"

func main() {
	debug := flag.Bool("debug", false, "Run with debugger support")
	flag.Parse()
	if err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{Address: "registry.terraform.io/akr4/hue", Debug: *debug}); err != nil {
		log.Fatal(err)
	}
}
