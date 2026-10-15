# Generate import blocks

`hue-tf import-blocks` finds resources on the bridge that Terraform does not
manage yet and generates Terraform `import` blocks for them. It covers rooms,
zones, scenes, smart scenes and behavior instances. It does not generate
resource definitions, change or remove existing definitions, update state, or
write to the bridge. Let Terraform generate the definitions with
`terraform plan -generate-config-out=generated.tf`, or write them yourself.

## Usage

Run it from your Terraform root directory. It reads the state of the current
backend and workspace. `HUE_BRIDGE_HOST`, `HUE_BRIDGE_ID` and
`HUE_BRIDGE_APPLICATION_KEY` must point to the bridge that Terraform uses. If
the provider block sets `host`, `bridge_id` or `application_key` directly, use
the same literal values as the environment variables. An expression such as a
variable cannot be compared, so the command stops.

```sh
# Preview import blocks for unmanaged resources
hue-tf import-blocks

# Write them to imports_hue.tf in the root module
hue-tf import-blocks --write

# Limit the run to one or more UUIDs
hue-tf import-blocks UUID_A UUID_B --write

# Choose the module to import into
hue-tf import-blocks UUID_A --module downstairs.washroom --write
```

UUIDs that are already in state or in an existing import block are skipped.
Existing resources are never rewritten, even if they were changed or deleted
on the bridge. When you name a managed UUID or a full resource address, the
command reports that it skipped it. A UUID named twice produces one block. If a
named target is not found, nothing is written.

New import blocks all go into one `imports_hue.tf` in the root module, even
when they target a module. The command appends to the file and never edits or
removes existing blocks, so running it again before you import does not
duplicate them. Existing Hue import blocks are read from the root module's `.tf`
and `.tf.json` files, and their IDs must be literal strings. A Hue import block
that uses `for_each` stops the command until the import is done, because its
IDs cannot be read.

## Destination and names

Blocks target the root module by default. `--module downstairs.washroom` means
`module.downstairs.module.washroom`, following each module block's `source`.
**`--module` does not filter by Hue room.** Without UUIDs, the candidates are
all unmanaged resources on the bridge. The command does not guess which room
belongs to which Terraform module.

State and configuration are only read, so any layout works. That includes
count and for_each instances, deposed objects, provider aliases, `hue_device`,
modules with shared, remote or out-of-tree sources, override files and
`.tf.json`. The contents of remote modules are not read.

Only `--module` has a restriction: it must name a module with a local source.
If that module or a parent uses `count` or `for_each`, the import address would
need an instance key, so the command stops and says why.

Names come from `metadata.name`. Letters in any script, digits, `_` and `-` are
kept, and anything else becomes `_`. A name that starts with a digit gets a
`resource_` prefix. If an address collides with an existing definition or
another import, the UUID is appended to keep it unique.

## Writing and recovery

The preview changes no files. `--write` checks every target before it writes
anything. The only Terraform commands it runs are `terraform state pull` and
`terraform show -json`, which read state, so `terraform` must be on your
`PATH`. The resource definitions for the imports do not exist yet, so it does
not run `validate`.

Before writing, it saves recovery information in `.hue-pull-transaction`. The
name is kept for compatibility with the old command's check for an
interrupted run.

- On success, it removes the recovery information and leaves no other files
  behind.
- On failure, it restores the files, unless someone else edited them in the
  meantime. If it cannot restore them, it keeps the recovery information and
  refuses to run again.
- After an interruption, check `manifest.json` and the backups and repair the
  files. Then move the transaction directory aside and run the command again.

Add `.hue-pull-*` to `.gitignore`.

The old `pull` command's baseline, `.hue-pull-baseline.json`, is neither used
nor updated. The old `pull` command prints a migration message and exits, and
`import-blocks` does not synchronize existing resources. `--new UUID ADDRESS`
remains as a compatibility syntax for an explicit address, but a normal run
does not need `--new`.
