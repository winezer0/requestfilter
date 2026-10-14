package requestfilter

import (
	"net/http"
	"testing"
)

func TestIgnoreEmptyDropsCompletelyParsedRequestsWithoutParameters(t *testing.T) {
	tests := []struct {
		name    string
		request Request
	}{
		{
			name:    "request without query or body",
			request: Request{Method: "GET", URL: "https://example.test/", BodyCaptured: true},
		},
		{
			name: "empty form body",
			request: Request{
				Method: "POST", URL: "https://example.test/",
				Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, BodyCaptured: true,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := FilterBatch([]Request{test.request, test.request}, Options{ParameterPolicy: ParameterPolicyIgnoreEmpty})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Accepted) != 0 || len(result.Decisions) != 2 {
				t.Fatalf("FilterBatch() = (%#v, %v), want no accepted requests", result, err)
			}
			for _, decision := range result.Decisions {
				if decision.Action != ActionDrop || decision.Reason != ReasonNoParameters || decision.ParameterStatus != ParameterStatusComplete {
					t.Fatalf("decision = %#v, want complete no-parameters drop", decision)
				}
			}
		})
	}
}

func TestIgnoreEmptyKeepsParameterizedRequestsOnExactDedupPath(t *testing.T) {
	options := Options{ParameterPolicy: ParameterPolicyIgnoreEmpty}
	requests := []Request{
		{Method: "GET", URL: "https://example.test/search?q=one", BodyCaptured: true},
		{Method: "GET", URL: "https://example.test/search?q=two", BodyCaptured: true},
		{Method: "GET", URL: "https://example.test/search?q=two", BodyCaptured: true},
	}
	result, err := FilterBatch(requests, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accepted) != 2 {
		t.Fatalf("len(Accepted) = %d, want 2", len(result.Accepted))
	}
	if result.Decisions[0].Action != ActionAllow || result.Decisions[0].Reason != ReasonNewRequest || result.Decisions[1].Action != ActionAllow {
		t.Fatalf("parameterized decisions = %#v, want distinct requests allowed", result.Decisions[:2])
	}
	if decision := result.Decisions[2]; decision.Action != ActionDrop || decision.Reason != ReasonExactDuplicate {
		t.Fatalf("repeated decision = %#v, want exact duplicate", decision)
	}
}

func TestIgnoreEmptyIncompleteParsingFallsBackToExactDedup(t *testing.T) {
	session, err := NewSession(Options{ParameterPolicy: ParameterPolicyIgnoreEmpty})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	request := Request{
		Method: "POST", URL: "https://example.test/api",
		Header:       http.Header{"Content-Type": {"application/json"}},
		BodyCaptured: true, Body: []byte(`{"id":`),
	}
	decision, token, err := session.Reserve(request)
	if err != nil || decision.Action != ActionAllow || decision.ParameterStatus != ParameterStatusPartial {
		t.Fatalf("Reserve(malformed) = (%#v, %v), want partial allow", decision, err)
	}
	if err := session.Commit(token); err != nil {
		t.Fatal(err)
	}
	decision, _, err = session.Reserve(request)
	if err != nil || decision.Action != ActionDrop || decision.Reason != ReasonExactDuplicate {
		t.Fatalf("Reserve(repeated malformed) = (%#v, %v), want exact duplicate", decision, err)
	}
}
