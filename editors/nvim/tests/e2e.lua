-- End-to-end test of the plugin against a real bem-ls binary.
--
--   nvim --headless -u NONE -l tests/e2e.lua /path/to/bem-ls
--
-- This script drives a child Neovim over RPC so that input (insert mode,
-- <Tab>) goes through the main loop exactly like typing. Exits 0 on
-- success, 1 on failure.
local bin = arg[1]
assert(bin and vim.fn.executable(bin) == 1, 'usage: nvim --headless -u NONE -l tests/e2e.lua /path/to/bem-ls')
bin = vim.fn.fnamemodify(bin, ':p')
local plugin = vim.fs.dirname(vim.fs.dirname(vim.fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':p')))

local failures = 0
local function check(cond, msg)
  io.stdout:write((cond and 'ok   ' or 'FAIL ') .. msg .. '\n')
  if not cond then
    failures = failures + 1
  end
end

-- A small project.
local root = vim.fn.tempname()
vim.fn.mkdir(root .. '/.git', 'p')
local function write(name, text)
  local f = assert(io.open(root .. '/' .. name, 'w'))
  f:write(text)
  f:close()
end
write('bem.config.json', '{ "misc": { "diagnosticsDelay": 10 } }')
write('Card.tsx', [[
import './Card.scss';
export const Card = () => (
  <div className="card">
    <h2 className="card__title">Hi</h2>
    <p className="card__body" />
  </div>
);
]])
write('Card.scss', '.card {\n  &__title {}\n\n}\n')

-- The child editor.
local chan = vim.fn.jobstart({ 'nvim', '--embed', '--headless', '-u', 'NONE', '-i', 'NONE', '-n' }, { rpc = true })
local function lua(code, ...)
  return vim.rpcrequest(chan, 'nvim_exec_lua', code, { ... })
end
local function input(keys)
  vim.rpcrequest(chan, 'nvim_input', keys)
end
local function wait(ms, code, ...)
  local args = { ... }
  return vim.wait(ms, function()
    return lua(code, unpack(args))
  end, 30)
end

lua(
  [[
  local plugin, bin = ...
  vim.opt.runtimepath:prepend(plugin)
  vim.cmd('filetype on')
  require('bem-ls').setup({ cmd = { bin } })
  ]],
  plugin,
  bin
)

-- The component: diagnostics and go to definition.
lua('vim.cmd.edit(...)', root .. '/Card.tsx')
check(lua('return vim.bo.filetype') == 'typescriptreact', 'tsx filetype')
check(wait(5000, "return require('bem-ls').client() ~= nil"), 'client attaches to the component')
check(
  wait(
    5000,
    [[
    for _, d in ipairs(vim.diagnostic.get(0)) do
      if d.code == 'missingInStylesheet' and d.message:find('card__body', 1, true) then return true end
    end
    return false
  ]]
  ),
  'missingInStylesheet for card__body'
)
local def = lua([[
  local client = require('bem-ls').client()
  local line = 3
  local col = vim.api.nvim_buf_get_lines(0, line, line + 1, false)[1]:find('card__title', 1, true)
  local res = client:request_sync('textDocument/definition', {
    textDocument = { uri = vim.uri_from_bufnr(0) },
    position = { line = line, character = col },
  }, 3000, 0)
  local loc = res and res.result and res.result[1]
  return loc and { uri = loc.uri, line = loc.range.start.line } or vim.NIL
]])
check(type(def) == 'table' and def.uri:match('Card%.scss$') and def.line == 1, 'definition of card__title is in Card.scss')

-- :BemSkeleton opens a scratch buffer.
lua("vim.cmd('BemSkeleton')")
check(wait(3000, "return vim.bo.filetype == 'scss' and vim.bo.buftype == 'nofile'"), ':BemSkeleton opens a scss buffer')
local skeleton = lua("return table.concat(vim.api.nvim_buf_get_lines(0, 0, -1, false), '\\n')")
check(skeleton == '.card {\n  &__title {}\n  &__body {}\n}', ':BemSkeleton content ' .. vim.inspect(skeleton))
lua("vim.cmd('close')")

-- The stylesheet: ghost text on the blank line inside .card, accepted with
-- <Tab>.
lua('vim.cmd.edit(...)', root .. '/Card.scss')
check(wait(5000, "return require('bem-ls').client() ~= nil"), 'client attaches to the stylesheet')
check(lua('return vim.lsp.inline_completion.is_enabled({ bufnr = 0 })'), 'inline completion enabled for scss')
local map = lua("local m = vim.fn.maparg('<Tab>', 'i', false, true); return { buffer = m.buffer, desc = m.desc }")
check(map.buffer == 1 and map.desc == 'bem-ls: accept ghost text', '<Tab> mapped in insert mode')

lua('vim.api.nvim_win_set_cursor(0, { 3, 0 })') -- the blank line
input('i')
local shown = wait(
  5000,
  [[
  local ns = vim.api.nvim_create_namespace('nvim.lsp.inline_completion')
  return #vim.api.nvim_buf_get_extmarks(0, ns, 0, -1, {}) > 0
]]
)
check(shown, 'ghost text is displayed')
input('<Tab>')
-- The expr mapping applies the text just after the key; wait for it before
-- leaving insert mode (a person can't type <Esc> that fast).
wait(2000, [[return vim.api.nvim_buf_get_lines(0, 2, 3, false)[1] ~= '']])
input('<Esc>')
wait(2000, [[return not vim.api.nvim_get_mode().mode:find('i')]])
local text = lua("return table.concat(vim.api.nvim_buf_get_lines(0, 0, -1, false), '\\n')")
check(text == '.card {\n  &__title {}\n  &__body {}\n}', '<Tab> accepted the ghost text: ' .. vim.inspect(text))

-- Without a candidate, <Tab> is a normal tab.
lua('vim.api.nvim_buf_set_lines(0, 0, -1, false, { "x" })')
input('A<Tab><Esc>')
wait(2000, [[return not vim.api.nvim_get_mode().mode:find('i')]])
check(lua('return vim.api.nvim_buf_get_lines(0, 0, 1, false)[1]') == 'x\t', '<Tab> falls back to a tab')

-- :checkhealth runs without errors.
lua("vim.cmd('checkhealth bem-ls')")
local health = lua("return table.concat(vim.api.nvim_buf_get_lines(0, 0, -1, false), '\\n')")
check(health:find('OK Neovim', 1, true) and health:find('OK server:', 1, true) and not health:find('ERROR', 1, true), ':checkhealth bem-ls')

vim.fn.jobstop(chan)
vim.fn.delete(root, 'rf')
if failures > 0 then
  io.stdout:write(failures .. ' failure(s)\n')
  os.exit(1)
end
io.stdout:write('all passed\n')
os.exit(0)
