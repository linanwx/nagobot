---
name: spotlight-search
description: Search files and metadata using macOS Spotlight (mdfind).
tags: [macos, search, files, utility]
---
# Spotlight Search

Search files using macOS Spotlight index via `mdfind` and inspect metadata with `mdls`. Much faster than `find` for indexed locations.

## Search by Filename

```
exec: mdfind -name "FILENAME"
```

## Search by Content (Full-Text)

```
exec: mdfind "SEARCH_TERM"
```

## Search Within a Specific Directory

```
exec: mdfind -onlyin ~/Documents "SEARCH_TERM"
```

```
exec: mdfind -onlyin ~/Desktop -name "report"
```

## Search by File Type

PDF files:
```
exec: mdfind "kMDItemContentType == 'com.adobe.pdf'"
```

Images:
```
exec: mdfind "kMDItemContentType == 'public.image'"
```

Word documents:
```
exec: mdfind "kMDItemContentType == 'org.openxmlformats.wordprocessingml.document'"
```

Applications:
```
exec: mdfind "kMDItemContentType == 'com.apple.application-bundle'"
```

## Search by Date

Modified today:
```
exec: mdfind "kMDItemFSContentChangeDate >= $time.today"
```

Modified in last 7 days:
```
exec: mdfind "kMDItemFSContentChangeDate >= $time.today(-7)"
```

Created this week:
```
exec: mdfind "kMDItemFSCreationDate >= $time.this_week"
```

## Search by Size

Files larger than 100MB:
```
exec: mdfind "kMDItemFSSize > 100000000"
```

## Combine Conditions

PDFs modified this week:
```
exec: mdfind "kMDItemContentType == 'com.adobe.pdf' && kMDItemFSContentChangeDate >= $time.this_week"
```

Images larger than 5MB:
```
exec: mdfind "kMDItemContentType == 'public.image' && kMDItemFSSize > 5000000"
```

## Inspect File Metadata

```
exec: mdls /path/to/file
```

Specific attribute:
```
exec: mdls -name kMDItemContentType /path/to/file
```

## Useful Metadata Attributes

- `kMDItemDisplayName` — display name
- `kMDItemContentType` — file type UTI
- `kMDItemFSSize` — file size in bytes
- `kMDItemFSCreationDate` — creation date
- `kMDItemFSContentChangeDate` — last modified
- `kMDItemAuthors` — document authors
- `kMDItemTitle` — document title
- `kMDItemKind` — human-readable file kind
- `kMDItemWhereFroms` — download source URL
- `kMDItemPixelWidth` / `kMDItemPixelHeight` — image dimensions

## Count Results

```
exec: mdfind -count "SEARCH_TERM"
```

## Limit Results

```
exec: mdfind "SEARCH_TERM" | head -20
```

## Notes

- `mdfind` uses the Spotlight index, so it's extremely fast but only covers indexed locations.
- Excluded directories (like `.git`, `node_modules`) may not appear in results.
- To rebuild the Spotlight index: `sudo mdutil -E /`.
- For searching file contents by code patterns, consider `rg` (ripgrep) or `grep` instead.
- Combine with `read_file` to inspect matched files after searching.
