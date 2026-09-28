-- Citations for every format: _quarto.yml turns Quarto's own citeproc run off
-- (citeproc: false) and this filter runs it instead, so it can act on the
-- result.
--
-- nocite hands every page the whole bibliography, which keeps a work's number
-- the same on every page, and citeproc appends that list to each page it
-- renders. In the PDF, one document, it lands once, in the References chapter.
-- On the site each chapter would carry its own copy: Quarto hides it, but the
-- search index still reads it as part of the chapter's last section, so a
-- search for any cited title would match every chapter. So in HTML the list is
-- dropped from every page but References. Citation links still land there:
-- Quarto points them at the References page after rendering.
function Pandoc(doc)
  doc = pandoc.utils.citeproc(doc)
  if quarto.doc.is_format("html") and not quarto.doc.input_file:match("references%.qmd$") then
    doc.blocks = doc.blocks:walk({
      Div = function(el)
        if el.identifier == "refs" then
          return {}
        end
      end,
    })
  end
  return doc
end
