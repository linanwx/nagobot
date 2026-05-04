# HTML diagram / chart (SVG-driven)

Use when the user needs an exact visual — architecture diagrams, flowcharts, data charts, timelines, org charts, or any image that benefits from pixel-perfect code rendering.

The shared workflow (write → `nagobot upload-html` → share URL) and the inline-only critical rules live in the parent `SKILL.md`. This file covers the SVG-specific patterns.

## SVG best practices

- **Use `viewBox`** instead of fixed `width`/`height` for responsive scaling.
- **Grid-aligned coordinates**: use multiples of 10 or 20 for clean alignment.
- **`<g>` groups** for logical units (nodes, edges, labels).
- **`<defs>`** for reusable elements (arrowheads, gradients, patterns).
- **`<text>`** with `text-anchor="middle"` and `dominant-baseline="central"` for centered labels.
- **`<marker>`** for arrow tips on lines/paths.
- **`rx` on `<rect>`** for rounded corners.

## Color palette

Define CSS custom properties for consistent theming:

```css
:root {
  --bg: #ffffff; --text: #1a1a1a;
  --primary: #4f46e5; --secondary: #06b6d4;
  --accent: #f59e0b; --border: #e5e7eb;
  --success: #22c55e; --error: #ef4444;
}
```

## HTML template

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

## Common SVG patterns

### Rounded box with label
```svg
<g transform="translate(100, 50)">
  <rect width="140" height="60" rx="8" fill="#4f46e5"/>
  <text x="70" y="30" text-anchor="middle" dominant-baseline="central"
        fill="white" font-family="system-ui" font-size="14">Label</text>
</g>
```

### Arrow connection
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

### Curved arrow (Bezier)
```svg
<path d="M 170 200 C 250 200, 250 300, 330 300" fill="none"
      stroke="#4f46e5" stroke-width="2" marker-end="url(#arrow)"/>
```

## Trigger conditions

- Architecture diagrams, system diagrams.
- Flowcharts, decision trees, state machines.
- Data charts (bar, line, pie, scatter).
- Timelines, roadmaps.
- Org charts, relationship graphs.
- Any visual that needs exact positioning and data accuracy.

## When to switch to `singlepage.md`

If the deliverable is a multi-section document with reading flow (text-heavy, multiple headings, prose paragraphs), wrapping everything in one giant SVG fights the medium — use `singlepage.md` and embed small SVG illustrations inside the page where needed.
