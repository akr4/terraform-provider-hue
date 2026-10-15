terraform {
  required_providers {
    hue = { source = "akr4/hue" }
  }
}

# The provider reads its settings from the environment. `hue-tf init` prints
# the commands that set them, for example:
#
#   export HUE_BRIDGE_HOST='192.168.1.10'
#   export HUE_BRIDGE_ID='001788fffe000000'
#   export HUE_BRIDGE_APPLICATION_KEY='...'
provider "hue" {}
