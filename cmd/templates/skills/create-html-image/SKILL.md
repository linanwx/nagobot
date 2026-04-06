---
name: create-html-image
description: Create precise images, diagrams, charts, or visualizations using HTML+SVG when the user needs an exact visual — architecture diagrams, flowcharts, data charts, timelines, org charts, or any image that benefits from pixel-perfect code rendering.
tags: [image, svg, html, diagram, chart, visualization]
---
# Create HTML Image

Create precise, publication-quality images using HTML + inline SVG. The result is uploaded as a public web page and the URL is shared with the user.

## Workflow

1. Create a self-contained HTML file using `write_file`
2. Upload it:
```
exec: {{WORKSPACE}}/bin/nagobot upload-html <file-path>
```
3. Share the returned URL with the user

## Critical Rules

- **Everything must be inline.** No external CSS, JS, fonts, or images. No `<link href>`, no `<script src>`, no `@import url()`. This is non-negotiable — external resources WILL fail.
- **Use `<meta charset="utf-8">`** in the `<head>`.
- **Use SVG `viewBox`** instead of fixed `width`/`height` for responsive scaling.
- **System fonts only**: `font-family: system-ui, -apple-system, sans-serif`. Add `"Noto Sans SC", "PingFang SC"` for Chinese text.
- **Output complete code.** Never truncate with "rest remains the same."
- **Save to** `{{WORKSPACE}}/media/` with a descriptive filename, e.g. `architecture-diagram.html`.

## SVG Best Practices

- **Grid-aligned coordinates**: use multiples of 10 or 20 for clean alignment
- **`<g>` groups** for logical units (nodes, edges, labels)
- **`<defs>`** for reusable elements (arrowheads, gradients, patterns)
- **`<text>`** with `text-anchor="middle"` and `dominant-baseline="central"` for centered labels
- **`<marker>`** for arrow tips on lines/paths
- **`rx` on `<rect>`** for rounded corners

## Color Palette

Define CSS custom properties for consistent theming:

```css
:root {
  --bg: #ffffff; --text: #1a1a1a;
  --primary: #4f46e5; --secondary: #06b6d4;
  --accent: #f59e0b; --border: #e5e7eb;
  --success: #22c55e; --error: #ef4444;
}
```

## HTML Template

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Descriptive Title</title>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body {
      font-family: system-ui, -apple-system, sans-serif;
      background: #fff;
      display: flex;
      justify-content: center;
      padding: 20px;
    }
  </style>
</head>
<body>
  <svg viewBox="0 0 800 600" xmlns="http://www.w3.org/2000/svg">
    <!-- Content here -->
  </svg>
</body>
</html>
```

## Common SVG Patterns

### Rounded Box with Label
```svg
<g transform="translate(100, 50)">
  <rect width="140" height="60" rx="8" fill="#4f46e5"/>
  <text x="70" y="30" text-anchor="middle" dominant-baseline="central"
        fill="white" font-family="system-ui" font-size="14">Label</text>
</g>
```

### Arrow Connection
```svg
<defs>
  <marker id="arrow" viewBox="0 0 10 10" refX="10" refY="5"
          markerWidth="6" markerHeight="6" orient="auto-start-reverse">
    <path d="M 0 0 L 10 5 L 0 10 z" fill="#4f46e5"/>
  </marker>
</defs>
<line x1="240" y1="80" x2="350" y2="80" stroke="#4f46e5"
      stroke-width="2" marker-end="url(#arrow)"/>
```

### Curved Arrow (Bezier)
```svg
<path d="M 170 200 C 250 200, 250 300, 330 300" fill="none"
      stroke="#4f46e5" stroke-width="2" marker-end="url(#arrow)"/>
```

## When to Use This Skill

- Architecture diagrams, system diagrams
- Flowcharts, decision trees
- Data charts (bar, line, pie)
- Timelines, roadmaps
- Org charts, relationship graphs
- Any visual that needs exact positioning and data accuracy

## When NOT to Use

- Photorealistic images (use image generation tools)
- Simple text that doesn't need visual layout
- Quick sketches that don't need precision
