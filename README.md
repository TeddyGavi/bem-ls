# bem-ls

Tools for BEM class names in React components and their stylesheets:

- **`bem-ls`**, a language server for JSX/TSX and SCSS/CSS, with clients for
  [VS Code](editors/vscode) and [Neovim](editors/nvim);
- **`jsx2scss`**, a command that turns a component's BEM classes into an SCSS
  skeleton.

Everything is written in Go with no third-party dependencies.

[![Open in Codespaces](https://github.com/codespaces/badge.svg)](
  https://codespaces.new/TeddyGavi/bem-ls-demo
)

## What it does

In components (`.tsx`, `.jsx`, and styled-components in `.ts`/`.js`):

- checks class names against a naming convention (`block__elem--mod` and
  others): invalid names, nested elements, modifiers without their base,
  elements outside their block, classes missing from the stylesheet,
  `styles.x` missing from a CSS module;
- completes classes in `className`, `clsx(...)` and after `styles.`;
- hover, go to definition, references and rename for classes, CSS-module
  references and styled components, across components and stylesheets;
- quick fixes: correct a name, add a missing selector, create the stylesheet.

In stylesheets (`.scss`, `.css`):

- **ghost text** for the selectors the paired components use but the
  stylesheet lacks — accept with Tab; an empty file gets the whole skeleton;
- definition, references, rename and hover (with resolved values) for
  `$variables`, mixins, functions, placeholders, `@use` namespaces and custom
  properties; outline, workspace symbols, folding; unused selectors shown
  faded.

## Install

Download `bem-ls` and `jsx2scss` for your platform from the GitHub
releases, or build them from a checkout (Go 1.23 or later):

```sh
make build      # writes bin/bem-ls and bin/jsx2scss
```

Editors:

- **VS Code**: install the extension (it bundles `bem-ls`). See
  [editors/vscode](editors/vscode/README.md).
- **Neovim 0.12+**: see [editors/nvim](editors/nvim/README.md).

## jsx2scss

```sh
jsx2scss Card.tsx > Card.scss
jsx2scss -nesting bem -indent tab < Card.tsx
```

```jsx
<article className={`card ${featured ? 'card--featured' : ''}`}>
  <h2 className="card__title">{title}</h2>
  <div className="card__body"><p className="card__text">{children}</p></div>
</article>
```

```scss
.card {
  &--featured {}
  &__title {}
  &__body {}
  &__text {}
}
```

Run `jsx2scss -h` for all flags. It reads the same `bem.config.json` as the
language server.

## Configuration

A `bem.config.json` at the project root configures both tools and every
editor (it overrides editor settings):

```json
{
  "naming": { "preset": "classic" },
  "rules": { "nestedElement": "off" },
  "ignore": ["^js-"]
}
```

See [docs/configuration.md](docs/configuration.md) for every option and
[schema/bem.config.schema.json](schema/bem.config.schema.json) for the JSON
schema.

## Development

```sh
make            # lint and race tests
make fuzz       # every fuzz target (FUZZTIME=20s each)
make bench      # benchmarks; make bench-compare BASE=origin/main
make vscode     # check and bundle the VS Code extension
make nvim-e2e   # end-to-end test of the Neovim plugin
make snapshot   # local release build into dist/ (nothing is published)
```

Layout:

| Path | Contents |
|---|---|
| `internal/core` | JSX/TSX lexer and parser: elements, class references, imports, styled-components |
| `internal/bem` | Naming conventions and rules |
| `internal/generator` | SCSS skeleton generation |
| `internal/scss` | SCSS/CSS lexer and parser: rules, classes, symbols, references |
| `internal/index` | Workspace index: pairing components with stylesheets, the `@use` graph, cross-file resolution |
| `internal/config` | Settings and `bem.config.json` |
| `internal/lsp` | The language server; `jsonrpc` and `protocol` beneath it |
| `cmd/bem-ls`, `cmd/jsx2scss` | The binaries |
| `editors/` | VS Code, Neovim and Zed clients |

Releases are described in [docs/releasing.md](docs/releasing.md).

## License

[MIT](LICENSE)
