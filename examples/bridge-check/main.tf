terraform {
  required_version = ">= 1.5.0"
  required_providers {
    hue = { source = "akr4/hue" }
  }
}

# Credentials come from HUE_BRIDGE_HOST and HUE_BRIDGE_APPLICATION_KEY.
provider "hue" {}
