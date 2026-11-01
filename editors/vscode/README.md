# osb for VS Code

Editing support for [osb](https://github.com/SvenTo/osb) configurations
(`osb.yaml`, `*.osb.yaml` and YAML libraries with `steps:`, `actions:` or
`tests:`).

- **Completion** for configuration keys, step keys, action names (built-in,
  extra and the custom actions of the file), action arguments and values
  (flavors, targets, booleans, enums).
- **Documentation on hover** for actions, their arguments and every key.
- **Validation**: `osb validate` runs on save (or while typing, see
  `osb.validate`) and its errors are shown in the editor.
- **Commands** (command palette, `osb:`): render the configuration as a shell
  script, Dockerfile or cloud-init user-data next to the editor; validate; show
  the documentation of an action.

The action catalog is read from the installed `osb` binary
(`osb actions list -o json`), so completion matches your osb version; a bundled
catalog is used when osb is not installed. Validation and rendering need the
binary: download it from the
[releases](https://github.com/SvenTo/osb/releases).

## Settings

| Setting | Default | |
|---|---|---|
| `osb.path` | `osb` | Path of the osb binary |
| `osb.validate` | `onSave` | `onSave`, `onType` or `off` |
| `osb.flavor` | | Flavor for the render commands (empty: ask) |
| `osb.files` | `**/osb.yaml`, `**/*.osb.yaml`, `**/osb.*.yaml` | Globs of osb configurations |

If you also use the Red Hat YAML extension with osb's JSON schema
(`# yaml-language-server: $schema=...`), some keys are offered twice; remove
the schema comment or keep both.

## Development

```bash
make vscode-files   # copies the shared language support and the catalog
cd editors/vscode && npm test
make vscode         # builds dist/osb.vsix
```

Install the package with `code --install-extension dist/osb.vsix`.

The language support (`lib/osb-lang.js`) is shared with the website and
lives in `site/static/js/osb-lang.js`.
