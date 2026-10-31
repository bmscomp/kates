-- Kates YAML Language Server (`kates lsp --stdio`) integration for Neovim 0.10+
-- Usage in init.lua:
--   dofile(vim.fn.getcwd() .. "/editors/neovim-kates/kates_lsp.lua").setup()

local M = {}

function M.setup(opts)
  opts = opts or {}
  local cmd = opts.cmd or { "kates", "lsp", "--stdio" }

  local group = vim.api.nvim_create_augroup("KatesLSP", { clear = true })
  vim.api.nvim_create_autocmd("FileType", {
    group = group,
    pattern = { "yaml", "yaml.kates" },
    callback = function(args)
      local root_dir = vim.fs.dirname(
        vim.fs.find({ ".git", "schemas", "kates" }, { upward = true, path = args.file })[1]
      ) or vim.fn.getcwd()

      local client_id = vim.lsp.start({
        name = "kates_lsp",
        cmd = cmd,
        root_dir = root_dir,
      })

      if client_id and vim.lsp.inlay_hint then
        vim.lsp.inlay_hint.enable(true, { bufnr = args.buf })
      end
    end,
  })

  vim.api.nvim_create_user_command("KatesPreflight", function()
    local params = { uri = vim.uri_from_bufnr(0) }
    vim.lsp.buf_request(0, "kates/clusterPreflight", params, function(err, result)
      if err then
        vim.notify("Kates pre-flight error: " .. tostring(err.message), vim.log.levels.ERROR)
        return
      end
      if result and result.summary then
        vim.notify(result.summary, result.reachable and vim.log.levels.INFO or vim.log.levels.WARN)
      end
    end)
  end, { desc = "Run live Kates cluster pre-flight and blast-radius preview" })

  vim.api.nvim_create_user_command("KatesDetectDialect", function()
    local params = { uri = vim.uri_from_bufnr(0) }
    vim.lsp.buf_request(0, "kates/detectDialect", params, function(err, result)
      if not err and result then
        vim.notify(string.format("Kates dialect: %s (%s)", result.dialect, result.schema), vim.log.levels.INFO)
      end
    end)
  end, { desc = "Show detected Kates YAML dialect and schema" })
end

return M
