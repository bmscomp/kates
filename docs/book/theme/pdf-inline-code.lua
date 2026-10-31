-- PDF only: a long path, setting or flag in inline code may break after
-- / . - _ : = (and at its spaces), so justified text never pushes it into the
-- margin. Code shorter than 18 characters stays whole. Runs after
-- theme/pdf-tables.lua, which measures the Code inlines this turns into LaTeX.
if not quarto.doc.is_format("pdf") then
  return {}
end

local ESC = {
  ["\\"] = "\\textbackslash{}", ["{"] = "\\{", ["}"] = "\\}", ["$"] = "\\$", ["&"] = "\\&",
  ["#"] = "\\#", ["^"] = "\\^{}", ["_"] = "\\_", ["%"] = "\\%", ["~"] = "\\~{}",
}
local BREAK_AFTER = { ["/"] = true, ["."] = true, ["-"] = true, ["_"] = true, [":"] = true, ["="] = true, ["|"] = true }

function Code(el)
  if #el.text < 18 then
    return nil
  end
  local out = {}
  for i = 1, #el.text do
    local c = el.text:sub(i, i)
    if c == " " then
      out[#out + 1] = "\\ " -- Pandoc's own spacing inside code, and a legal break
    elseif c == "-" and el.text:sub(i + 1, i + 1) == "-" then
      out[#out + 1] = "-\\/" -- no -- ligature, as Pandoc writes it
    else
      out[#out + 1] = ESC[c] or c
    end
    local nxt = el.text:sub(i + 1, i + 1)
    -- after a separator, or inside camelCase (kubernetesServiceDnsDomain)
    if i < #el.text and (BREAK_AFTER[c] or (c:match("%l") and nxt:match("%u"))) then
      out[#out + 1] = "\\allowbreak{}"
    end
  end
  return pandoc.RawInline("latex", "\\texttt{" .. table.concat(out) .. "}")
end
