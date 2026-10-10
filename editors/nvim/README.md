# bem-ls.nvim

Neovim client for `bem-ls`: BEM checks, completion and navigation in
JSX/TSX, and ghost text for missing selectors in SCSS/CSS.

Requires **Neovim 0.12** (it uses `vim.lsp.config`, `vim.lsp.enable` and
`vim.lsp.inline_completion`) and the `bem-ls` binary from the GitHub releases
on your `PATH`.

## Install

With lazy.nvim:

```lua
{
  'TeddyGavi/scss-bem-gen',
  config = function(plugin)
    vim.opt.rtp:append(plugin.dir .. '/editors/nvim')
    require('bem-ls').setup({
      settings = { naming = { preset = 'classic' } },
    })
  end,
}
```

Or add `editors/nvim` to `runtimepath` some other way and call `setup()`.

Without `setup()`, `vim.lsp.enable('bem_ls')` uses the defaults in
`lsp/bem_ls.lua` (no ghost text mapping, no commands).

## Options

```lua
require('bem-ls').setup({
  cmd = { 'bem-ls' },        -- server command
  filetypes = nil,           -- default: tsx/jsx/ts/js/scss/css/sass
  settings = {},             -- the "bem" settings; see docs/configuration.md
  ghost_text = true,         -- inline suggestions in stylesheets
  accept_key = '<Tab>',      -- insert-mode key that accepts them (false: none)
  on_attach = nil,           -- function(client, bufnr)
})
```

A `bem.config.json` at the project root overrides `settings`.

## Use

- In a stylesheet, go to a blank line inside `.block { }` (or anywhere in an
  empty file): the missing selectors appear as ghost text; `<Tab>` accepts.
  The same edit is available with `vim.lsp.buf.code_action()`.
- `:BemSkeleton` shows the SCSS skeleton of the current component.
- `:checkhealth bem-ls` checks the setup.

## Tests

```sh
go build -o /tmp/bem-ls ./cmd/bem-ls
nvim --headless -u NONE -l editors/nvim/tests/e2e.lua /tmp/bem-ls
```
