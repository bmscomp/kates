-- PDF only: the back-of-book index. Reads index-terms.txt, the list that
-- scripts/gen-book-index.sh builds the site's Index page from, and marks each
-- section that teaches a term (uses it twice or more, or names it in its
-- heading; a term no section uses twice is marked where it is used most) with
-- \index{term} at the term's first paragraph there, so the printed index lists
-- that page. The Index page's own heading goes: \printindex sets the chapter.
if not quarto.doc.is_format("pdf") then return {} end

local root = quarto.project.directory or "."
local fh = io.open(pandoc.path.join({ root, "index-terms.txt" }), "r")
if not fh then
  quarto.log.warning("index-entries.lua: no index-terms.txt, so the PDF gets no index")
  return {}
end

local function trim(s) return (s:gsub("^%s+", ""):gsub("%s+$", "")) end
local function split(s, sep)
  local out = {}
  for part in (s .. sep):gmatch("(.-)" .. sep) do
    part = trim(part)
    if part ~= "" then out[#out + 1] = part end
  end
  return out
end
local function escape_latex(s)
  return (s:gsub("[%%&#_{}$]", "\\%0"))
end

local entries = {}
for raw in fh:lines() do
  local line = trim((raw:gsub("#.*$", "")))
  if line ~= "" and not line:find("%->") then
    local parts = split(line, ";")
    local head, parent = parts[1], nil
    if head:find(">") then
      local p = split(head, ">")
      parent, head = p[1], p[2]
    end
    local words = { head }
    for i = 2, #parts do words[#words + 1] = parts[i] end
    local patterns = {}
    for _, w in ipairs(words) do
      patterns[#patterns + 1] = w:lower():gsub("(%W)", "%%%1"):gsub("%s+", "%%s+")
    end
    entries[#entries + 1] = {
      key = parent and (escape_latex(parent) .. "!" .. escape_latex(head)) or escape_latex(head),
      patterns = patterns,
    }
  end
end
fh:close()

local function count_matches(text, e)
  local t = " " .. text:lower() .. " "
  local n = 0
  for _, pat in ipairs(e.patterns) do
    local s = 1
    while true do
      local a, b = t:find(pat, s)
      if not a then break end
      if not t:sub(a - 1, a - 1):match("[%w_`]") and not t:sub(b + 1, b + 1):match("[%w_`]") then n = n + 1 end
      s = b + 1
    end
  end
  return n
end

local PROSE = { Para = true, Plain = true, BulletList = true, OrderedList = true, DefinitionList = true, BlockQuote = true }

-- Both passes walk the document the same way, so a prose block is named by
-- its position: (section number, ordinal in the section). Pandoc hands a
-- filter fresh Lua objects on every access, so object identity cannot be
-- the key.
local function traverse(blocks, state, on_block)
  for _, b in ipairs(blocks) do
    if b.t == "Header" then
      if b.level == 1 then
        local id = b.identifier or ""
        local title = pandoc.utils.stringify(b)
        state.skip = (id == "glossary" or id == "references" or id == "index" or title:match("^Part [IVX]+") ~= nil)
      end
      if b.level <= 2 then
        state.si = state.si + 1
        state.ordinal = 0
        state.sections[state.si] = { heading = pandoc.utils.stringify(b):lower(), counts = {},
          skip = state.skip or pandoc.utils.stringify(b):lower():match("^summary") ~= nil }
      end
    elseif b.t == "Div" and not b.classes:includes("callout") then
      traverse(b.content, state, on_block)
    elseif state.si > 0 and (PROSE[b.t] or (b.t == "Div" and b.classes:includes("callout"))) then
      state.ordinal = state.ordinal + 1
      on_block(b, state.si, state.ordinal)
    end
  end
end

function Pandoc(doc)
  -- Pass 1: count each term per section.
  local st = { si = 0, ordinal = 0, sections = {}, skip = false }
  local texts = {} -- "si:ordinal" -> text
  traverse(doc.blocks, st, function(b, si, ordinal)
    local s = st.sections[si]
    if s.skip then return end
    local text = pandoc.utils.stringify(b)
    texts[si .. ":" .. ordinal] = text
    for i, e in ipairs(entries) do
      local n = count_matches(text, e)
      if n > 0 then s.counts[i] = (s.counts[i] or 0) + n end
    end
  end)
  local sections = st.sections
  for _, s in ipairs(sections) do
    if not s.skip then
      for i, e in ipairs(entries) do
        if count_matches(s.heading, e) > 0 then s.counts[i] = (s.counts[i] or 0) + 2 end
      end
    end
  end
  -- Which sections each entry is indexed in: twice or more, or named in the
  -- heading; otherwise the one or two sections that use it most.
  local chosen = {} -- si -> { entry index, ... }
  for i = 1, #entries do
    local strong, best = {}, {}
    for si, s in ipairs(sections) do
      local n = s.counts[i] or 0
      if n >= 2 then strong[#strong + 1] = si end
      if n > 0 then best[#best + 1] = { si = si, n = n } end
    end
    if #strong == 0 then
      table.sort(best, function(a, b) return a.n > b.n end)
      for k = 1, math.min(2, #best) do strong[#strong + 1] = best[k].si end
    end
    for _, si in ipairs(strong) do
      chosen[si] = chosen[si] or {}
      table.insert(chosen[si], i)
    end
  end
  -- Where each mark goes: the first block of the section that holds the term.
  local marks = {} -- "si:ordinal" -> { key, ... }
  for si, list in pairs(chosen) do
    for _, i in ipairs(list) do
      local e = entries[i]
      local placed = nil
      local ordinal = 1
      while texts[si .. ":" .. ordinal] do
        if count_matches(texts[si .. ":" .. ordinal], e) > 0 then placed = ordinal; break end
        ordinal = ordinal + 1
      end
      placed = placed or 1
      local k = si .. ":" .. placed
      marks[k] = marks[k] or {}
      table.insert(marks[k], "\\index{" .. e.key .. "}")
    end
  end
  -- Pass 2: the same walk, inserting the marks and dropping the Index page's
  -- heading (\printindex sets the chapter).
  local st2 = { si = 0, ordinal = 0, sections = {}, skip = false }
  local function rewrite(blocks)
    local out = pandoc.List()
    for _, b in ipairs(blocks) do
      if b.t == "Header" then
        if b.level == 1 then
          st2.skip = false -- keeps the state shape of pass 1
        end
        if b.level <= 2 then
          st2.si = st2.si + 1
          st2.ordinal = 0
        end
        if not (b.level == 1 and b.identifier == "index") then out:insert(b) end
      elseif b.t == "Div" and not b.classes:includes("callout") then
        b.content = rewrite(b.content)
        out:insert(b)
      elseif st2.si > 0 and (PROSE[b.t] or (b.t == "Div" and b.classes:includes("callout"))) then
        st2.ordinal = st2.ordinal + 1
        local m = marks[st2.si .. ":" .. st2.ordinal]
        if m then
          if b.t == "Para" or b.t == "Plain" then
            b.content:insert(1, pandoc.RawInline("latex", table.concat(m)))
          else
            out:insert(pandoc.RawBlock("latex", table.concat(m)))
          end
        end
        out:insert(b)
      else
        out:insert(b)
      end
    end
    return out
  end
  return pandoc.Pandoc(rewrite(doc.blocks), doc.meta)
end
