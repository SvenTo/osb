# osb documentation

1. [Getting started](getting-started.md): create, validate, render, build, apply
2. [Configuration reference](configuration.md): arguments, flavors, targets,
   artifacts, steps, secrets
3. [Expressions and templates](expressions.md): `${{ }}`, conditions,
   pre-scripts, validations, helper functions
4. [Targets](targets.md): shell, Dockerfile, OCI, WSL, cloud-init, qcow2, OVA, apply
5. [Libraries](libraries.md): custom actions, imports, git + lockfile, tests

Generated references (`make docs`):

- [Built-in and extra actions](reference/actions.md)
- [Target settings](reference/settings.md)
- [Command line](reference/cli.md)
- [JSON schema](osb.schema.json)
