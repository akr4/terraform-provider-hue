#!/usr/bin/env python3
"""Prepare a private local workspace without connecting to a Hue bridge."""
import json
import os
import shlex
from pathlib import Path


def main():
    os.umask(0o077)
    repo = Path(__file__).resolve().parent.parent
    for binary in ("hue-tf", "terraform-provider-hue"):
        if not os.access(repo / "bin" / binary, os.X_OK):
            raise SystemExit("Build the local binaries first: make build")
    target = repo / ".local" / "bridge-check"
    target.mkdir(parents=True, exist_ok=True, mode=0o700)
    templates = repo / "examples" / "bridge-check"
    for name in ("main.tf", "resources.tf.example"):
        destination = target / name
        if not destination.exists():
            destination.write_bytes((templates / name).read_bytes())
    config = target / "dev.tfrc"
    if not config.exists():
        config.write_text(
            "provider_installation {\n"
            "  dev_overrides {\n"
            '    "registry.terraform.io/akr4/hue" = '
            + json.dumps(str(repo / "bin"))
            + "\n  }\n}\n",
            encoding="utf-8",
        )
    environment = target / "env.sh"
    if not environment.exists():
        environment.write_text(
            "# Source this file before running Terraform in this directory.\n"
            + "export TF_CLI_CONFIG_FILE=" + shlex.quote(str(config)) + "\n"
            + "export TF_DATA_DIR=" + shlex.quote(str(target / ".terraform")) + "\n"
            + "export TF_WORKSPACE=default\n",
            encoding="utf-8",
        )
    (target / "captures").mkdir(exist_ok=True, mode=0o700)
    print(f"Prepared: {target}")
    print("Existing files preserved. No bridge requests were made.")
    print(f"cd {shlex.quote(str(target))}")
    print(f"source {shlex.quote(str(environment))}")
    print("terraform validate")


if __name__ == "__main__":
    main()
