// Soft-keyboard compensation for iOS.
//
// What each browser does when the virtual keyboard opens differs, and only one
// of the two cases is broken for us (per the CSS viewport spec author's
// explainer, https://github.com/bramus/viewport-resize-behavior):
//
//   Android Chrome/Firefox/Edge — resize BOTH the visual and the layout
//     viewport, so `dvh` shrinks with the keyboard and a bottom-anchored
//     composer stays visible on its own. `interactive-widget=resizes-content`
//     in the viewport meta makes that behaviour declared rather than inherited
//     from a UA default.
//
//   iOS Safari — resizes ONLY the visual viewport. The layout viewport, and
//     therefore `100dvh`/`100vh`, does NOT change. A `h-dvh` flex column keeps
//     its full height behind the keyboard and the composer at its bottom edge
//     ends up underneath it; Safari then scroll-nudges the focused field into
//     view, which is what makes the whole layout look mis-measured.
//     `interactive-widget` cannot fix this — WebKit has not implemented it
//     (bug 259770), so the VisualViewport API is the only route.
//
// So: measure how much of the layout viewport the keyboard covers and publish
// it as `--keyboard-inset`, which the root height subtracts.
//
// The formula is self-cancelling by construction: it compares the LAYOUT
// viewport (documentElement.clientHeight) against the VISUAL one. Where both
// shrink together (Android) the difference stays ~0 and nothing is subtracted
// twice; where only the visual one shrinks (iOS) the difference is exactly the
// keyboard's height.
export function trackKeyboardInset(): () => void {
  const vv = window.visualViewport;
  if (!vv) return () => {};

  const root = document.documentElement;
  let raf = 0;

  const update = () => {
    raf = 0;
    // offsetTop is how far the visual viewport has been scrolled down inside
    // the layout viewport; without it, a Safari scroll-into-view nudge reads
    // as the keyboard having shrunk.
    const covered = root.clientHeight - (vv.height + vv.offsetTop);
    // Sub-pixel noise and browser-chrome transitions produce small values that
    // are not a keyboard; ignore anything under a plausible keyboard height.
    const inset = covered > 40 ? Math.round(covered) : 0;
    root.style.setProperty("--keyboard-inset", `${inset}px`);
    // Zeroes --safe-bottom while open: the keyboard already covers the home
    // indicator, so the inset would only add a gap above the keys.
    if (inset > 0) root.setAttribute("data-keyboard", "open");
    else root.removeAttribute("data-keyboard");
  };

  const schedule = () => {
    if (raf) return;
    raf = requestAnimationFrame(update);
  };

  vv.addEventListener("resize", schedule);
  vv.addEventListener("scroll", schedule);
  update();

  return () => {
    if (raf) cancelAnimationFrame(raf);
    vv.removeEventListener("resize", schedule);
    vv.removeEventListener("scroll", schedule);
    root.style.removeProperty("--keyboard-inset");
    root.removeAttribute("data-keyboard");
  };
}
