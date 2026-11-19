---
note: "read_file on a PDF: PDFs are not read natively. Rendered as ONE line: line breaks here are joined with spaces."
---
This is a PDF document.
PDFs are not read natively.
Extract it first, then read the result: use the exec tool to render pages to images (e.g.
pdftoppm / pdftocairo / ImageMagick) and read_file those images (vision is supported), or extract text (e.g.
pdftotext).
If no such tool is available on this host, say so instead of guessing the contents.
