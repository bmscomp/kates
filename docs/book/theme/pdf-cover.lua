-- PDF only: open the book on its cover. Draws assets/cover.svg into a PDF with
-- rsvg-convert, the tool Quarto itself uses for SVG images in a PDF, and tells
-- theme/pdf-header.tex where it is; the header's \maketitle puts it on a page
-- of its own, full bleed, before the usual title page. Without rsvg-convert
-- the book keeps the usual title page alone, and the render says why.

if not quarto.doc.is_format("pdf") then
  return {}
end

-- Written into Quarto's own scratch directory, which git ignores. LaTeX runs
-- in the project directory, so the header can use the relative path.
local COVER = "assets/cover.svg"
local TARGET = ".quarto/kates-cover.pdf"

function Pandoc(doc)
  local root = quarto.project.directory or "."
  pcall(pandoc.system.make_directory, pandoc.path.join({ root, ".quarto" }), true)
  local ok, err = pcall(pandoc.pipe, "rsvg-convert", {
    "-f", "pdf",
    "-o", pandoc.path.join({ root, TARGET }),
    pandoc.path.join({ root, COVER }),
  }, "")
  if ok then
    quarto.doc.include_text("in-header", "\\newcommand*{\\katescoverfile}{" .. TARGET .. "}")
  else
    quarto.log.warning("No cover page in the PDF: rsvg-convert could not draw " .. COVER
      .. " (" .. (type(err) == "table" and ("exit code " .. tostring(err.error_code)) or tostring(err)) .. ")")
  end
  return doc
end
