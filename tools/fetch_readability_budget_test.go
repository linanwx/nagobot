package tools

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// bigPage builds a page whose article body sits after padBytes of filler, the
// shape of every real page that hit this bug: WeChat, and any site that inlines
// scripts, styles or base64 images ahead of its content. The filler is markup
// readability will discard, so the extracted article is small no matter how
// large the input — which is the whole reason an input cap is the wrong tool
// for bounding this provider's output.
func bigPage(padBytes int, body string) []byte {
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html><head><title>Big Page</title></head><body>")
	filler := `<script>var x="` + strings.Repeat("A", 900) + `";</script>`
	for sb.Len() < padBytes {
		sb.WriteString(filler)
	}
	sb.WriteString(`<article>` + body + `</article></body></html>`)
	return []byte(sb.String())
}

// TestReadabilityExtractsPastTheOldReadCap is the regression that produced 326
// "the Node field is nil" failures across the fleet. The article body sits past
// webFetchMaxReadBytes (500 KB); under that cap the document was cut off before
// it and readability found no article at all.
func TestReadabilityExtractsPastTheOldReadCap(t *testing.T) {
	sentence := "The Vodafone Ireland outage began mid-afternoon and reports climbed steeply. " +
		"Engineers restored service later the same evening after a nationwide interruption. "
	body := "<p>" + strings.Repeat(sentence, 12) + "</p>"

	page := bigPage(webFetchMaxReadBytes+50_000, body)
	if len(page) <= webFetchMaxReadBytes {
		t.Fatalf("fixture is %d bytes, must exceed the old %d-byte cap", len(page), webFetchMaxReadBytes)
	}

	srv := serveBytes(t, "text/html; charset=utf-8", page)
	ctx, _ := fetchCtx(t)

	got, err := (&ReadabilityFetchProvider{}).Fetch(ctx, srv.URL+"/article")
	if err != nil {
		t.Fatalf("Fetch failed on a %d-byte page: %v", len(page), err)
	}
	if !strings.Contains(got, "Vodafone Ireland outage") {
		t.Errorf("article body missing from extraction; got %d chars: %.200q", len(got), got)
	}
	// The point of a large INPUT budget: the output stays small regardless.
	if len(got) > 10_000 {
		t.Errorf("extracted %d chars from a %d-byte page; extraction is not bounding output", len(got), len(page))
	}
}

// TestReadabilityNamesWhyExtractionFailed pins that a nil Node is reported as
// what happened, not as the library's internal "the Node field is nil". Both
// RenderHTML and RenderText check that same field, so the render fallback could
// never rescue this case — it only relabelled one opaque message as another,
// and the model could not tell "this page has no article" (no source will help)
// from "this page was too big for us" (nothing was wrong with the page).
func TestReadabilityNamesWhyExtractionFailed(t *testing.T) {
	// A page with a head but no article body at all.
	srv := serveBytes(t, "text/html; charset=utf-8",
		[]byte(`<!DOCTYPE html><html><head><title>Empty</title></head><body></body></html>`))
	ctx, _ := fetchCtx(t)

	_, err := (&ReadabilityFetchProvider{}).Fetch(ctx, srv.URL+"/empty")
	if err == nil {
		t.Fatal("expected an error for a page with no article body")
	}
	var contentErr *ContentError
	if !errors.As(err, &contentErr) {
		t.Fatalf("err = %v, want *ContentError so it grades SeverityWarn", err)
	}
	if strings.Contains(err.Error(), "Node field") {
		t.Errorf("leaked the library's internal message: %v", err)
	}
	if !strings.Contains(err.Error(), "no article body") {
		t.Errorf("err = %q, want it to name the cause", err)
	}
}

// TestReadabilityReportsTruncationDistinctly is the other half: io.LimitReader
// gives no signal that it clipped, so without reading one byte past the budget
// an over-budget page is indistinguishable from one that genuinely has no
// article — and the model would be told to try another source when no source
// can help with a page we ourselves cut short.
func TestReadabilityReportsTruncationDistinctly(t *testing.T) {
	// Body past the extraction budget, so the parse sees only filler.
	page := bigPage(webFetchMaxExtractBytes+1_000, "<p>"+strings.Repeat("unreachable body. ", 40)+"</p>")
	srv := serveBytes(t, "text/html; charset=utf-8", page)
	ctx, _ := fetchCtx(t)

	_, err := (&ReadabilityFetchProvider{}).Fetch(ctx, srv.URL+"/huge")
	if err == nil {
		t.Fatal("expected an error when the article body is past the budget")
	}
	var contentErr *ContentError
	if !errors.As(err, &contentErr) {
		t.Fatalf("err = %v, want *ContentError", err)
	}
	if !strings.Contains(err.Error(), "extraction budget") {
		t.Errorf("err = %q, want it to name truncation as the cause", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprint(webFetchMaxExtractBytes)) {
		t.Errorf("err = %q, want it to state the budget", err)
	}
}

// TestExtractBudgetExceedsReadCap states the relationship the fix rests on: the
// extractor's input budget must stay well clear of the cap meant for providers
// whose body IS their output. Lowering it back to webFetchMaxReadBytes silently
// reintroduces the original failure on every page over 500 KB.
func TestExtractBudgetExceedsReadCap(t *testing.T) {
	if webFetchMaxExtractBytes <= webFetchMaxReadBytes {
		t.Fatalf("extract budget %d must exceed the read cap %d", webFetchMaxExtractBytes, webFetchMaxReadBytes)
	}
	// Largest page seen across the 642 real URLs this deployment has fetched
	// (the 3.46 MB WeChat page that surfaced this was not even the biggest).
	// Anything at or below this is not headroom, it is the same bug with a
	// larger constant.
	const largestObservedPage = 5_970_000
	if webFetchMaxExtractBytes <= largestObservedPage {
		t.Fatalf("extract budget %d does not clear the largest observed real page (%d bytes)", webFetchMaxExtractBytes, largestObservedPage)
	}
}
