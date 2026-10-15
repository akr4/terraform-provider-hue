terraform {
  required_providers {
    hue = { source = "akr4/hue" }
  }
}

# Set HUE_BRIDGE_HOST, HUE_BRIDGE_ID and HUE_BRIDGE_APPLICATION_KEY in your
# environment. `hue-tf init` prints all three.
provider "hue" {}
