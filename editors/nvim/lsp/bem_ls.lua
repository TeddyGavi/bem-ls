-- Default configuration for the bem-ls language server. Neovim 0.11+ finds
-- this file on 'runtimepath', so `vim.lsp.enable('bem_ls')` works on its
-- own; require('bem-ls').setup() adds ghost text and commands.
return {
  cmd = { 'bem-ls' },
  filetypes = { 'typescriptreact', 'javascriptreact', 'typescript', 'javascript', 'scss', 'css', 'sass' },
  root_markers = { 'bem.config.json', 'package.json', '.git' },
  -- Sent as initializationOptions and answered to workspace/configuration
  -- (section "bem"). A bem.config.json at the root overrides both.
  init_options = {},
  settings = { bem = {} },
}
