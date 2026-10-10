# BEM for JSX & SCSS

Checks the BEM class names in React components against their stylesheets,
and helps write the stylesheets. Backed by the `bem-ls` language server.

## Features

In `.tsx` / `.jsx` (and styled-components in `.ts` / `.js`):

- Diagnostics: invalid names, nested elements, modifiers without their base,
  elements outside their block, classes missing from the stylesheet,
  `styles.x` missing from a CSS module.
- Completion in `className`, `clsx(...)` and after `styles.`.
- Hover, go to definition and find references for classes, `styles.x` and
  styled components; rename a class across components and stylesheets.
- Quick fixes: correct a name, add a missing selector, create the stylesheet.
- **BEM: Generate SCSS skeleton** opens the skeleton for the current
  component.

In `.scss` / `.css`:

- **Ghost text**: on a blank line, the missing selectors for the paired
  components appear; press Tab to accept. An empty stylesheet gets the whole
  skeleton. The same edit is the code action "Insert missing BEM selectors".
- Definition, references, rename and hover (with resolved values) for
  `$variables`, mixins, functions, placeholders, `@use` namespaces and custom
  properties. Outline, workspace symbols, folding, highlights.
- Unused selectors are shown faded.

## Configuration

Settings live under `bem.*` and mirror `bem.config.json`. A
`bem.config.json` at the workspace root overrides editor settings and is
shared with the CLI and other editors; the extension validates it against
its schema.

```json
{
  "$schema": "https://raw.githubusercontent.com/TeddyGavi/scss-bem-gen/main/schema/bem.config.schema.json",
  "naming": { "preset": "classic" },
  "rules": { "nestedElement": "off" },
  "ignore": ["^js-"],
  "stylesheet": { "aliases": { "@/": "src/" } }
}
```

## The server binary

The platform-specific extension bundles `bem-ls`. To use another build, set
`bem.server.path` (it supports `~` and `${workspaceFolder}`), or put
`bem-ls` on your `PATH`. **BEM: Restart server** restarts it.

## Development

```sh
npm install
npm run dev-binary   # go build ../../cmd/bem-ls into bin/
npm run build        # bundle into dist/
npm test             # unit tests and the settings/schema check
npx vsce package --target darwin-arm64
```

`npm run sync-settings` regenerates the `bem.*` settings in `package.json`
from `schema/bem.config.schema.json`.
