package tools

import (
	"errors"
	"fmt"
	"testing"
)

func TestRemoteErrorSeverity(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorSeverity
	}{
		// A third-party host refusing us is not a defect in this program.
		{"403 anti-bot", &HTTPError{StatusCode: 403, Status: "403 Forbidden"}, SeverityInfo},
		{"404", &HTTPError{StatusCode: 404, Status: "404 Not Found"}, SeverityInfo},
		{"429", &HTTPError{StatusCode: 429, Status: "429 Too Many Requests"}, SeverityInfo},
		{"503", &HTTPError{StatusCode: 503, Status: "503 Service Unavailable"}, SeverityInfo},

		// jina and kimi format a source prefix around the status. They must
		// wrap with %w, or errors.As misses them and their 403s get logged as
		// our defect — which is exactly what happened before they were wrapped.
		{"jina wrapped 403", fmt.Errorf("jina reader: %w", &HTTPError{StatusCode: 403, Status: "403 Forbidden"}), SeverityInfo},
		{"kimi wrapped 500", fmt.Errorf("kimi fetch: %w", &HTTPError{StatusCode: 500, Status: "500 Internal Server Error"}), SeverityInfo},

		// Fetched fine, could not extract — recoverable by trying another source.
		{"readability no article", &ContentError{Err: errors.New("the Node field is nil")}, SeverityWarn},
		{"content error wrapped", fmt.Errorf("render: %w", &ContentError{Err: errors.New("boom")}), SeverityWarn},

		// Anything we cannot positively explain stays an error on purpose.
		{"dns failure", errors.New("dial tcp: no such host"), SeverityError},
		{"tls failure", errors.New("x509: certificate has expired"), SeverityError},
		{"timeout", errors.New("context deadline exceeded"), SeverityError},

		// A 3xx is not a refusal and has no business being quietly downgraded.
		{"301 not downgraded", &HTTPError{StatusCode: 301, Status: "301 Moved Permanently"}, SeverityError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := remoteErrorSeverity(c.err); got != c.want {
				t.Fatalf("remoteErrorSeverity(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

// The severity must survive the trip through the result string, because that
// string is the only channel between the tool and the runner that logs it.
func TestToolErrorSeverityRoundTrip(t *testing.T) {
	for _, sev := range []ErrorSeverity{SeverityInfo, SeverityWarn, SeverityError} {
		res := toolErrorSev("web_fetch", sev, "boom")
		if !IsToolError(res) {
			t.Fatalf("severity %q: must still count as a tool error", sev)
		}
		if got := ToolErrorSeverity(res); got != sev {
			t.Fatalf("round trip: got %q, want %q", got, sev)
		}
	}
}

// Downgrading the log level must not change what the model sees: the result is
// still status:error with an "Error: " body, still fed back into the turn.
func TestDowngradedErrorStillReachesModel(t *testing.T) {
	res := toolErrorSev("web_fetch", SeverityInfo, "HTTP 403 403 Forbidden")
	if !IsToolError(res) {
		t.Fatal("an info-severity failure must still be a tool error")
	}
	if !contains(res, "status: error") {
		t.Fatalf("must keep status: error; got:\n%s", res)
	}
	if !contains(res, "Error: HTTP 403") {
		t.Fatalf("must keep the Error: body; got:\n%s", res)
	}
}

// Tools that have not opted in — and every legacy bare "Error: ..." string —
// keep the old ERROR level.
func TestUnclassifiedErrorsDefaultToError(t *testing.T) {
	if got := ToolErrorSeverity(toolError("edit_file", "could not find the exact text")); got != SeverityError {
		t.Fatalf("toolError default = %q, want %q", got, SeverityError)
	}
	if got := ToolErrorSeverity("Error: unknown argument(s): max_results"); got != SeverityError {
		t.Fatalf("legacy bare string = %q, want %q", got, SeverityError)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
