# BEM for Zed

Registers the `bem-ls` language server for TSX, JavaScript, TypeScript, CSS
and SCSS (SCSS needs Zed's SCSS extension).

You get diagnostics, completion, hover, go to definition, references,
rename, code actions, document symbols and highlights. Zed has no generic
LSP inline completion yet, so there is no ghost text: use the code action
**Insert missing BEM selectors** in a stylesheet instead.

## The server

The extension uses, in order:

1. `lsp.bem-ls.binary.path` from your settings,
2. `bem-ls` on your `PATH`,
3. the latest release from GitHub, downloaded automatically.

## Settings

Put server settings under `lsp.bem-ls` in Zed's `settings.json`, or in a
`bem.config.json` at the project root (which wins):

```json
{
  "lsp": {
    "bem-ls": {
      "settings": {
        "naming": { "preset": "classic" },
        "rules": { "nestedElement": "off" }
      }
    }
  }
}
```

## Development

Install Rust via rustup, then in Zed run **zed: install dev extension** and
pick this directory. `cargo test` runs the unit tests.

Publishing is a pull request to
[zed-industries/extensions](https://github.com/zed-industries/extensions)
adding this directory as a submodule entry (see that repository's README).
