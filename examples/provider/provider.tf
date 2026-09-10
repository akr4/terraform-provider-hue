terraform {
  required_providers {
    hue = { source = "akr4/hue" }
  }
}

# Set HUE_BRIDGE_HOST and HUE_BRIDGE_APPLICATION_KEY in your environment.
provider "hue" {}
