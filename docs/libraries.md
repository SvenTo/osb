# Libraries, imports and tests

## Extra actions

osb ships a library of higher-level actions written in this format, compiled
into the binary: for example `install-aur-package`, `mkdir-home`,
`letsencrypt-certificate`, `plasma-autologin` or `krdp-server`. They can be used
without an import; see [Extra actions](reference/actions.md#extra-actions) for
the list and `osb actions source <action>` for the YAML. Their sources and tests
are in [`library/`](../library) and are a good starting point for your own
libraries. A configuration that defines an action with the same name replaces
the extra action.

## Custom actions

Custom actions bundle steps behind a name and arguments. They can be defined in
the configuration itself or in imported files:

```yaml
actions:
  setup-dotfiles:
    description: Clone dotfiles and link them
    arguments:                       # same format as top-level arguments
      user: { type: string, required: true }
      repo: { default: "https://github.com/${{ args.user }}/dotfiles.git" }
    flavors: [ubuntu, debian]        # optional: supported flavors
    output:                          # becomes steps.<name>.output of the caller
      path: /home/${{ args.user }}/.dotfiles
    steps:
      - action: git-clone
        args:
          repo: ${{ args.repo }}
          dest: /home/${{ args.user }}/.dotfiles
          user: ${{ args.user }}
      - action: run-command
        args:
          command: [/home/${{ args.user }}/.dotfiles/install.sh]
          user: ${{ args.user }}

steps:
  - name: dotfiles
    action: setup-dotfiles
    args: { user: alice }
  - action: log
    args: { message: "dotfiles in ${{ steps.dotfiles.output.path }}" }
```

Inside a custom action, `args` are the action's arguments and `root.args` the
top-level arguments. Step names are scoped to the action. Relative `source`
paths (for example of `write-file`) resolve against the file that defines the
action. Recursion is detected and reported.

The generated output shows the nesting: `Step 4/9: Dotfiles › git-clone`.

## Imports

```yaml
imports:
  - common.yaml                      # relative to this file
  - library/*.yaml                   # globs; *.test.yaml files are skipped
  - git: https://github.com/example/osb-library.git
    ref: v1.2.0                      # tag, branch or commit (default: HEAD)
    path: actions/*.yaml             # glob inside the repository (default: *.yaml)
```

An imported file can contribute:

- `actions` (a name may only be defined once),
- `arguments` (only if the importing configuration does not define them),
- `flavors` and `artifacts` settings (the importing configuration wins),
- `steps`, which run **before** the steps of the importing file, in import order.

Imports are recursive; each file is loaded once.

## Git imports and the lockfile

Git repositories are fetched with a built-in git client (no `git` binary
needed) into `~/.cache/osb/git`. The resolved commit of every git import is
pinned in `osb.lock` next to the root configuration:

```yaml
# osb lockfile: pins git imports to commits. Update with `osb imports update`.
imports:
  "https://github.com/example/osb-library.git@v1.2.0":
    commit: 3f1c...
```

Commit the lockfile to get reproducible builds. `osb imports update` resolves
all refs again, rewrites the lockfile and removes unused entries.
`osb imports list` prints the files the configuration consists of and the
pinned commits. A new import (or a changed `ref`) is locked automatically the
first time it is used.

## Tests

A library file `foo.yaml` can have tests in `foo.test.yaml` next to it; the
library is imported automatically. Run all tests below a directory with:

```bash
osb test library/
```

```yaml
# foo.test.yaml
arguments:                     # arguments the tests can set
  user: { default: alice }

tests:
  - name: renders the profile
    flavor: ubuntu             # default: the first enabled flavor (alphabetically)
    target: shell              # shell (default), dockerfile, cloud-init, ...
    arguments: { user: bob }
    steps:
      - action: setup-dotfiles
        args: { user: "${{ args.user }}" }
    expect:
      contains: ["git clone"]
      not-contains: ["sudo"]
      warnings: []             # substrings of expected generation warnings
    run: true                  # with --run: execute the script in a container

  - name: rejects bad input
    steps:
      - action: setup-dotfiles
        args: { user: "" }
    expect:
      error: "does not match pattern"

  - name: golden output
    flavor: archlinux
    steps:
      - action: setup-dotfiles
        args: { user: bob }
    expect:
      golden: testdata/dotfiles-archlinux.sh
```

- `contains` / `not-contains` check the rendered output (the script, the
  Dockerfile or the `user-data`).
- `golden` compares the complete output with a file; `osb test --update`
  (re)writes golden files. Paths in the output are relative to the test file,
  so golden files don't depend on the working directory.
- `error` expects generation to fail with a message containing the text.
- `run: true` tests are executed with `osb test --run`: the shell script runs in
  a container of the flavor's base image and must exit successfully.

Missing required arguments get placeholder values in tests, so library actions
can be tested without providing every top-level argument.
