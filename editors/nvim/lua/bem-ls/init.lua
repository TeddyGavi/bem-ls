--- bem-ls.nvim: BEM class checks and SCSS ghost text for React components.
---
--- Requires Neovim 0.12 or later (vim.lsp.config, vim.lsp.enable and
--- vim.lsp.inline_completion).
local M = {}

local NAME = 'bem_ls'
local STYLE_FILETYPES = { scss = true, css = true, sass = true }

---@class bem_ls.Options
---@field cmd? string[] Command to start the server; default { 'bem-ls' }.
---@field filetypes? string[] Filetypes to attach to.
---@field root_markers? string[] Files that mark the workspace root, in
---priority order (the first marker found wins). Overrides the default in
---lsp/bem_ls.lua; use it to root at a monorepo root instead of a package.
---@field settings? table Server settings (the "bem" section), e.g. { naming = { preset = 'kv' } }.
---@field ghost_text? boolean Show missing selectors as ghost text in stylesheets; default true.
---@field accept_key? string|false Insert-mode key that accepts ghost text; default '<Tab>'. false: no mapping.
---@field on_attach? fun(client: vim.lsp.Client, bufnr: integer)

---@type bem_ls.Options
local defaults = {
  settings = {},
  ghost_text = true,
  accept_key = '<Tab>',
}

M.options = vim.deepcopy(defaults)

local function notify(msg, level)
  vim.notify('bem-ls: ' .. msg, level or vim.log.levels.INFO)
end

--- Returns the bem-ls client attached to a buffer, if any.
---@param bufnr? integer
---@return vim.lsp.Client?
function M.client(bufnr)
  return vim.lsp.get_clients({ bufnr = bufnr or 0, name = NAME })[1]
end

local function on_attach(client, bufnr)
  local opts = M.options
  if opts.ghost_text and STYLE_FILETYPES[vim.bo[bufnr].filetype] then
    vim.lsp.inline_completion.enable(true, { bufnr = bufnr })
    if opts.accept_key then
      local key = opts.accept_key
      vim.keymap.set('i', key, function()
        if not vim.lsp.inline_completion.get({ bufnr = bufnr }) then
          return key
        end
      end, { buffer = bufnr, expr = true, desc = 'bem-ls: accept ghost text' })
    end
  end
  if opts.on_attach then
    opts.on_attach(client, bufnr)
  end
end

--- Configures and enables the server.
---@param opts? bem_ls.Options
function M.setup(opts)
  if vim.fn.has('nvim-0.12') == 0 then
    notify('requires Neovim 0.12 or later', vim.log.levels.ERROR)
    return
  end
  M.options = vim.tbl_deep_extend('force', vim.deepcopy(defaults), opts or {})
  local o = M.options

  -- An empty Lua table would be sent as a JSON array.
  local settings = vim.tbl_isempty(o.settings) and vim.empty_dict() or o.settings
  local cfg = {
    init_options = settings,
    settings = { bem = settings },
  }
  if o.cmd then
    cfg.cmd = o.cmd
  end
  if o.filetypes then
    cfg.filetypes = o.filetypes
  end
  if o.root_markers then
    cfg.root_markers = o.root_markers
  end
  vim.lsp.config(NAME, cfg)

  local group = vim.api.nvim_create_augroup('bem-ls', { clear = true })
  vim.api.nvim_create_autocmd('LspAttach', {
    group = group,
    callback = function(args)
      local client = vim.lsp.get_client_by_id(args.data.client_id)
      if client and client.name == NAME then
        on_attach(client, args.buf)
      end
    end,
  })

  vim.api.nvim_create_user_command('BemSkeleton', function()
    M.skeleton()
  end, { desc = 'Show the BEM SCSS skeleton of the current component' })

  vim.lsp.enable(NAME)
end

--- Opens a scratch buffer with the SCSS skeleton of the current component.
---@param bufnr? integer
function M.skeleton(bufnr)
  bufnr = (bufnr == nil or bufnr == 0) and vim.api.nvim_get_current_buf() or bufnr
  local client = M.client(bufnr)
  if not client then
    notify('not attached to this buffer', vim.log.levels.WARN)
    return
  end
  client:request('workspace/executeCommand', {
    command = 'bem.generateSkeleton',
    arguments = { vim.uri_from_bufnr(bufnr) },
  }, function(err, result)
    if err then
      notify(err.message, vim.log.levels.ERROR)
      return
    end
    if type(result) ~= 'string' or result == '' then
      notify('no BEM classes found in this component')
      return
    end
    vim.cmd('vnew')
    local out = vim.api.nvim_get_current_buf()
    vim.bo[out].buftype = 'nofile'
    vim.bo[out].bufhidden = 'wipe'
    vim.bo[out].swapfile = false
    vim.api.nvim_buf_set_lines(out, 0, -1, false, vim.split(result:gsub('\n$', ''), '\n'))
    vim.bo[out].filetype = 'scss'
  end, bufnr)
end

return M
