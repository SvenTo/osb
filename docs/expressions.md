# Expressions and templates

osb uses **JavaScript** (ES5.1 with many ES6 features, via
[goja](https://github.com/dop251/goja)) for all logic, with one template syntax:

```yaml
content: |
  Hello ${{ args.user }}, you have ${{ args.groups.length }} groups.
```

- `${{ expression }}` is replaced by the value of the expression.
- If a value consists of **exactly one** `${{ }}`, the raw value is used, so it
  can be a list, mapping, number or boolean:
  `packages: ${{ args.extra.concat(['git']) }}`.
- Other values are converted to text: `null`/`undefined` become an empty
  string, lists and mappings become JSON.
- `$${{` produces a literal `${{`.
- Plain shell syntax such as `$HOME` or `${VAR}` is left alone, so shell code can
  be written naturally.

## When expressions run

All expressions are evaluated **at generation time** (when you run `osb render`,
`osb build` or `osb apply`), not on the target machine. They can see the
arguments, the flavor, the target and outputs of earlier steps. They cannot see
the state of the machine that will be configured; use the run-time guards
`only-if`, `unless` and `creates` for that (see [configuration](configuration.md#run-time-guards-and-error-handling)).

## Where expressions are used

| Place | Form |
|---|---|
| `args`, `output`, `title`, guards, `artifacts`, argument defaults | templates: `${{ ... }}` |
| `if`, `pre-validation`, `post-validation` | plain JavaScript (a single `${{ }}` is also accepted) |
| `foreach` | a YAML list, plain JavaScript or a single `${{ }}` |
| `pre-script` | JavaScript statements |
| `template-file` sources | templates |

## Variables

| Name | Content |
|---|---|
| `args` | arguments (inside a custom action: the action's arguments) |
| `root.args` | top-level arguments (useful inside custom actions) |
| `flavor` | `name`, `family` (`debian` or `arch`), `basedOn`, `baseImage`, `vars` |
| `target` | `name`, `renderer` (`shell`, `dockerfile`, `cloud-init`), `live` (runs on a booted system), `image` (builds an image) |
| `steps` | `steps.<name>.enabled`, `steps.<name>.output` of earlier steps in the same scope |
| `item`, `index` | current element and position in a `foreach` |
| `output`, `rendered` | in `post-validation`: the step's output and its shell form |

Names with dashes need brackets: `args["extra-users"]`, `steps["add-user"].output`.

## Helper functions

| Function | Result |
|---|---|
| `shell(v)` | `v` quoted for POSIX sh |
| `json(v)` | JSON text |
| `yaml(v)` | YAML text |
| `b64(s)`, `unb64(s)` | Base64 encode / decode |
| `sha256(s)` | hex SHA-256 |
| `file(path)` | content of a local file, relative to the configuration |
| `env(name, default)` | environment variable of the machine running osb |
| `indent(s, n)` | indent non-empty lines by `n` spaces |
| `fail(message)` | abort generation with an error |

## pre-script

`pre-script` runs before the step's arguments are rendered. Variables it
defines are available in templates of the step (and of nested steps of a block):

```yaml
- title: Configure ${{ name }}
  pre-script: |
    var name = args.user.toLowerCase();
    var groups = args.admin ? ['sudo', 'docker'] : [];
  pre-validation: name.length <= 32
  action: add-user
  args:
    name: ${{ name }}
    groups: ${{ groups }}
```

## Validation

`pre-validation` and `post-validation` are assertions. A falsy result or an
exception (`throw new Error("...")` or `fail("...")`) stops generation with the
step's position:

```yaml
- action: write-file
  args: { path: /etc/app.conf, content: "${{ args.config }}" }
  post-validation: rendered.indexOf("password") < 0
```

## Examples

```yaml
if: flavor.family == "debian" && args.desktop
foreach: ${{ args.users.filter(function (u) { return u.admin; }) }}
args:
  name: ${{ item.name }}
  content: ${{ json({port: args.port, debug: false}) }}
  command: echo ${{ shell(args.message) }}
```
