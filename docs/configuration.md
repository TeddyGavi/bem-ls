# Configuration

`bem-ls` and `jsx2scss` share one set of settings. They come from, in order
of increasing priority:

1. the editor's `initializationOptions`;
2. the editor's settings (`workspace/didChangeConfiguration`, or the `bem`
   section of `workspace/configuration`);
3. **`bem.config.json` at the project root** — this wins, and is merged key
   by key over the editor's settings (lists are replaced, not appended).

`jsx2scss` reads only the file: `-config FILE`, or the nearest
`bem.config.json` found from the input file's directory upwards, stopping at
the directory containing `.git`. Its flags (`-naming`, `-indent`,
`-nesting`) override the file; `-no-config` skips it.

The server reloads the file when it changes. Mistakes are shown as a
diagnostic on the file and a warning, and the last good settings stay in
effect. Unknown keys in the file are errors (to catch typos); editors may
send extra keys of their own, which are ignored.

Editors validate the file against
[`schema/bem.config.schema.json`](../schema/bem.config.schema.json); add
`"$schema"` to get completion in any editor with JSON-schema support.

## Example

```json
{
  "$schema": "https://raw.githubusercontent.com/TeddyGavi/scss-bem-gen/main/schema/bem.config.schema.json",
  "naming": { "preset": "classic" },
  "rules": {
    "nestedElement": "off",
    "missingInStylesheet": "error"
  },
  "ignore": ["^js-", "^is-"],
  "stylesheet": {
    "aliases": { "@/": "src/" },
    "loadPaths": ["src/styles"]
  },
  "generator": { "indent": 2, "nesting": "dom" },
  "cssModules": { "identifiers": ["styles", "css"] }
}
```

## Reference

| Key | Default | Meaning |
|---|---|---|
| `naming.preset` | `classic` | `classic` (`b__e--m`), `kv` (`b__e--key_value`), `react` (`Block-elem_mod_value`), `custom` |
| `naming.elementSeparator`, `modifierSeparator`, `valueSeparator` | per preset | Separators (set these for `custom`) |
| `naming.block`, `element`, `modifier`, `value` | per preset | Regular expressions each part must match (anchors are added) |
| `rules.<rule>` | see below | `off`, `hint`, `info`, `warning`, `error` |
| `ignore` | `[]` | Regular expressions; matching classes are skipped by rules and skeletons |
| `stylesheet.lookup` | `["import","sibling","directory","parent"]` | How a component finds its stylesheet, in order |
| `stylesheet.extensions` | `.module.scss`, `.scss`, `.module.css`, `.css`, `.module.sass`, `.sass` | Suffixes tried for sibling/parent lookups |
| `stylesheet.maxSearchDepth` | `2` | Parent directories searched |
| `stylesheet.loadPaths` | `[]` | Extra Sass load paths (root-relative) |
| `stylesheet.aliases` | `{}` | Import prefixes, e.g. `{"@/": "src/"}`; applied to JS/stylesheet imports and Sass `@use`/`@forward`/`@import` URLs |
| `stylesheet.exclude` | `node_modules`, `.git`, `dist`, `build`, … | Directories not indexed |
| `stylesheet.globalFallback` | `true` | Resolve unreachable `$vars`/mixins to any global definition (injected partials) |
| `generator.indent` | `2` | Spaces, or `"tab"` |
| `generator.nesting` | `dom` | `dom`: follow the JSX tree; `bem`: one rule per block |
| `jsx.classAttributes` | `["className","class"]` | Attributes holding classes |
| `jsx.classFunctions` | `clsx`, `classnames`, `classNames`, `cx`, `cn`, `twMerge`, `twJoin` | Helpers whose arguments are classes |
| `cssModules.identifiers` | `["styles","classes"]` | Identifiers treated as CSS modules without an import (imported ones are detected) |
| `styledComponents.identifiers` | `["styled"]` | styled-components factories |
| `ghostText.enabled` | `true` | Ghost text in stylesheets |
| `misc.diagnosticsDelay` | `200` | Debounce after a change, in ms |
| `misc.scanOnStartup` | `true` | Index the workspace in the background |

### Rules

| Rule | Default | Reports |
|---|---|---|
| `invalidName` | warning | A class that doesn't follow the naming convention (with a suggested fix) |
| `nestedElement` | info | `b__e1__e2` |
| `modifierWithoutBase` | warning | `b--m` without `b` on the same element |
| `elementOutsideBlock` | warning | `b__e` with no ancestor using block `b` |
| `multipleBlocksMix` | info | Two different blocks on one element |
| `missingInStylesheet` | warning | A class the component's stylesheet doesn't define |
| `unusedSelector` | hint | A stylesheet class no linked component uses (faded) |
| `moduleClassMissing` | error | `styles.x` where the CSS module has no `.x` |

## Editors

- **VS Code** — settings `bem.*` mirror the keys above (`bem.naming.preset`,
  `bem.rules`, …), plus `bem.server.path` and `bem.trace.server`. See
  [`editors/vscode`](../editors/vscode/README.md).
- **Neovim** — `require('bem-ls').setup({ settings = { … } })`. See
  [`editors/nvim`](../editors/nvim/README.md).
- **Zed** — `lsp.bem-ls.settings` in `settings.json`. See
  [`editors/zed`](../editors/zed/README.md).
