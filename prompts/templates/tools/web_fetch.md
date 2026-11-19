---
note: "web_fetch tool. The body is the tool description; `params` are the parameter descriptions by dotted path. Types, enums and required lists stay in code."
params:
  limit: "Maximum number of characters to return. Default: 10000."
  offset: "Character offset to start returning content from. Use to paginate through long pages. Default: 0."
  source: "Fetch source. Empty to see guide."
  url: "The URL to fetch."
---
Fetch the content of a web page. Returns readable text/markdown. If the URL serves a file rather than a page (PDF, image, audio, archive, Office document), the file is downloaded to the workspace and the result gives its local path and type instead of page text. Content is cached for 10 minutes — repeated fetches of the same URL are free. Use offset/limit to paginate through long pages without re-fetching. If a site returns 403/503 (anti-bot), retry with a different source.
