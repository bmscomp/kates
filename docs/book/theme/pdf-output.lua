-- PDF only: set `text` fences (terminal output and ASCII screens) apart from
-- the commands around them, as the web theme does. Each becomes a light
-- `katesoutput` panel with a thin left rule (theme/pdf-header.tex) instead of
-- the tinted code block that commands get.
--
-- The panel is labelled "Output" when the paragraph just before the fence
-- introduces output: it ends in a colon and mentions output, like STYLE.md's
-- "Output:" lead-in, or starts with "Expected" ("Expected:"). Other `text`
-- fences (a file layout, a directory tree, a Lab screen) get the panel
-- without a label, since calling them output would be wrong.

if not quarto.doc.is_format("pdf") then
  return {}
end

local function introduces_output(block)
  if block == nil or block.t ~= "Para" then
    return false
  end
  local text = pandoc.utils.stringify(block)
  if not text:match(":%s*$") then
    return false
  end
  return text:lower():find("output", 1, true) ~= nil or text:match("^Expected") ~= nil
end

local function panel(code, labelled)
  local lines = { labelled and "\\begin{katesoutput}[output label=Output]" or "\\begin{katesoutput}" }
  lines[#lines + 1] = "\\begin{Verbatim}"
  lines[#lines + 1] = code.text
  lines[#lines + 1] = "\\end{Verbatim}"
  lines[#lines + 1] = "\\end{katesoutput}"
  return pandoc.RawBlock("latex", table.concat(lines, "\n"))
end

function Blocks(blocks)
  local changed = false
  for i, block in ipairs(blocks) do
    if block.t == "CodeBlock" and block.classes[1] == "text" then
      blocks[i] = panel(block, introduces_output(blocks[i - 1]))
      changed = true
    end
  end
  if changed then
    return blocks
  end
end
