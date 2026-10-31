-- PDF only: give every table column a width, so Pandoc sets wrapping p{}
-- columns, and never less than its longest unbreakable token needs, so a
-- setting or class name in a cell cannot run into the next column or the
-- margin. A pipe table whose rows fit Pandoc's 72 columns otherwise gets l
-- columns, which never wrap. Widths are estimated from character counts at
-- the tables' \small size on the A4 text block (pdf-header.tex, _quarto.yml).
if not quarto.doc.is_format("pdf") then
  return {}
end

local CODE_CW, TEXT_CW, TEXTWIDTH, COLSEP = 4.7, 5.2, 453.5, 6 -- points

-- The longest piece a cell can't break, and the cell's total length
local function measure(blocks)
  local longest, total = 0, 0
  pandoc.Div(blocks):walk({
    Code = function(c)
      total = total + #c.text * CODE_CW
      -- theme/pdf-inline-code.lua lets code break after / . - _ : =
      for seg in c.text:gmatch("[^%s/%.%-_:=]+[/%.%-_:=]?") do
        longest = math.max(longest, #seg * CODE_CW)
      end
    end,
    Str = function(s)
      total = total + #s.text * TEXT_CW
      longest = math.max(longest, #s.text * TEXT_CW)
    end,
    Space = function()
      total = total + TEXT_CW * 0.5
    end,
  })
  return longest, total
end

function Table(tbl)
  local n = #tbl.colspecs
  if n < 2 then
    return nil
  end
  local rows, minw, sum_total = 0, {}, {}
  for i = 1, n do
    minw[i], sum_total[i] = 0, 0
  end
  local function scan(list)
    for _, row in ipairs(list) do
      rows = rows + 1
      for i, cell in ipairs(row.cells) do
        if i <= n then
          local l, t = measure(cell.contents)
          minw[i] = math.max(minw[i], l)
          sum_total[i] = sum_total[i] + t
        end
      end
    end
  end
  scan(tbl.head.rows)
  for _, body in ipairs(tbl.bodies) do
    scan(body.body)
  end

  local has_widths = false
  for _, cs in ipairs(tbl.colspecs) do
    if cs[2] ~= nil then
      has_widths = true
    end
  end
  -- Pandoc sets a column to w * (textwidth - 2 n tabcolsep), so shares are
  -- measured against the width left once the padding is out
  local avail = TEXTWIDTH - 2 * n * COLSEP
  local w, need = {}, {}
  local sum, need_sum = 0, 0
  for i = 1, n do
    need[i] = (minw[i] + 3) / avail
    if has_widths then
      w[i] = math.max(tbl.colspecs[i][2] or 0, need[i])
    else
      local avg = (sum_total[i] / math.max(rows, 1) + 8) / avail
      w[i] = math.max(math.min(avg, 0.6), need[i], 0.07)
    end
    sum, need_sum = sum + w[i], need_sum + need[i]
  end
  -- Too wide: take the excess from the columns with room above their need,
  -- in proportion to that room. Only when every column is at its need does
  -- the whole table shrink, and then the cell's text wants rewording.
  if sum > 0.985 then
    if need_sum >= 0.985 then
      for i = 1, n do
        w[i] = w[i] / sum * 0.985
      end
    else
      local slack = 0
      for i = 1, n do
        slack = slack + (w[i] - need[i])
      end
      local excess = sum - 0.985
      for i = 1, n do
        w[i] = w[i] - (w[i] - need[i]) / slack * excess
      end
    end
  end
  for i = 1, n do
    tbl.colspecs[i] = { tbl.colspecs[i][1], w[i] }
  end
  return tbl
end
