-- :checkhealth bem-ls
local M = {}

function M.check()
  local h = vim.health
  h.start('bem-ls')
  if vim.fn.has('nvim-0.12') == 1 then
    h.ok('Neovim ' .. tostring(vim.version()))
  else
    h.error('Neovim 0.12 or later is required', 'Upgrade Neovim')
  end

  local cfg = vim.lsp.config.bem_ls or {}
  local cmd = type(cfg.cmd) == 'table' and cfg.cmd[1] or 'bem-ls'
  if vim.fn.executable(cmd) == 1 then
    local out = vim.fn.system({ cmd, '-version' })
    h.ok(('server: %s (%s)'):format(vim.fn.exepath(cmd), vim.trim(out)))
  else
    h.error(('%s is not executable'):format(cmd), {
      'Download bem-ls from the GitHub releases and put it on your PATH',
      "or pass cmd = { '/path/to/bem-ls' } to require('bem-ls').setup()",
    })
  end

  local clients = vim.lsp.get_clients({ name = 'bem_ls' })
  if #clients > 0 then
    h.ok(('%d client(s) running; root %s'):format(#clients, clients[1].root_dir or '?'))
    local config = vim.fs.find('bem.config.json', { upward = true, path = clients[1].root_dir })[1]
    if config then
      h.info('configuration: ' .. config)
    end
  else
    h.info('no client running (open a .tsx or .scss file)')
  end
end

return M
