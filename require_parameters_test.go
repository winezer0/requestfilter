package requestfilter

import (
	"net/http"
	"testing"
	"time"
)

// PARAM intentionally differs from the reference's early filter: the first
// complete empty-parameter request passes here and only committed repeats drop.
func TestRequireParametersAllowsFirstAndDropsRepeatedCompleteNoParameterRequests(t *testing.T) {
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
			result, err := FilterBatch([]Request{test.request, test.request}, Options{DedupStrategy: DedupStrategyRequireParameters})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Accepted) != 1 || len(result.Decisions) != 2 {
				t.Fatalf("FilterBatch() = (%#v, %v), want first request accepted", result, err)
			}
			if decision := result.Decisions[0]; decision.Action != ActionAllow || decision.ParameterStatus != ParameterStatusComplete {
				t.Fatalf("first decision = %#v, want complete no-parameters allow", decision)
			}
			if decision := result.Decisions[1]; decision.Action != ActionDrop || decision.Reason != ReasonNoParameters || decision.ParameterStatus != ParameterStatusComplete {
				t.Fatalf("second decision = %#v, want repeated complete no-parameters drop", decision)
			}
		})
	}
}

func TestRequireParametersGroupsEmptyRequestsByAuthenticationAndEndpoint(t *testing.T) {
	options := Options{DedupStrategy: DedupStrategyRequireParameters}
	request := Request{Method: "GET", URL: "https://example.test/", BodyCaptured: true}
	requests := []Request{
		request,
		request,
		{Method: "GET", URL: "https://example.test/other", BodyCaptured: true},
		{Method: "GET", URL: "https://example.test/", AuthenticationGroupID: "account-b", BodyCaptured: true},
		{Method: "GET", URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, BodyCaptured: true},
		{Method: "GET", URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, BodyCaptured: true},
		{Method: "POST", URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, BodyCaptured: true},
		{Method: "POST", URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, BodyCaptured: true},
	}
	result, err := FilterBatch(requests, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accepted) != 5 {
		t.Fatalf("len(Accepted) = %d, want 5", len(result.Accepted))
	}
	wantActions := []Action{ActionAllow, ActionDrop, ActionAllow, ActionAllow, ActionAllow, ActionDrop, ActionAllow, ActionDrop}
	for index, want := range wantActions {
		if got := result.Decisions[index].Action; got != want {
			t.Fatalf("decision[%d].Action = %q, want %q; decisions = %#v", index, got, want, result.Decisions)
		}
	}
}

func TestRequireParametersPendingEmptyRequestCanBeRetried(t *testing.T) {
	session, err := NewSession(Options{DedupStrategy: DedupStrategyRequireParameters})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	request := Request{Method: "GET", URL: "https://example.test/", BodyCaptured: true}
	decision, reservation, err := session.Reserve(request)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(first) = (%#v, %v), want allow", decision, err)
	}
	decision, _, err = session.Reserve(request)
	if err != nil || decision.Action != ActionRetry || decision.Reason != ReasonInFlight {
		t.Fatalf("Reserve(in-flight duplicate) = (%#v, %v), want retry", decision, err)
	}
	if err := session.Release(reservation); err != nil {
		t.Fatal(err)
	}
	decision, reservation, err = session.Reserve(request)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(after release) = (%#v, %v), want allow", decision, err)
	}
	if err := session.Commit(reservation); err != nil {
		t.Fatal(err)
	}
	decision, _, err = session.Reserve(request)
	if err != nil || decision.Action != ActionDrop || decision.Reason != ReasonNoParameters {
		t.Fatalf("Reserve(after commit) = (%#v, %v), want no-parameters duplicate drop", decision, err)
	}
}

func TestRequireParametersEmptyRequestStateExpires(t *testing.T) {
	session, err := NewSession(Options{DedupStrategy: DedupStrategyRequireParameters, EntryTTL: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	request := Request{Method: "GET", URL: "https://example.test/", BodyCaptured: true}
	decision, reservation, err := session.Reserve(request)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(first) = (%#v, %v), want allow", decision, err)
	}
	if err := session.Commit(reservation); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	decision, _, err = session.Reserve(request)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(after TTL) = (%#v, %v), want allow", decision, err)
	}
}

func TestRequireParametersEmptyRequestUsesSessionCapacity(t *testing.T) {
	session, err := NewSession(Options{DedupStrategy: DedupStrategyRequireParameters, MaxEntries: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	first := Request{Method: "GET", URL: "https://example.test/first", BodyCaptured: true}
	second := Request{Method: "GET", URL: "https://example.test/second", BodyCaptured: true}
	decision, reservation, err := session.Reserve(first)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(first) = (%#v, %v), want allow", decision, err)
	}
	if err := session.Commit(reservation); err != nil {
		t.Fatal(err)
	}
	decision, _, err = session.Reserve(second)
	if err != nil || decision.Action != ActionReject || decision.Reason != ReasonCapacity {
		t.Fatalf("Reserve(second) = (%#v, %v), want capacity reject", decision, err)
	}
}

func TestRequireParametersAllowsAllParameterizedRequests(t *testing.T) {
	options := Options{DedupStrategy: DedupStrategyRequireParameters}
	requests := []Request{
		{Method: "GET", URL: "https://example.test/search?q=one", BodyCaptured: true},
		{Method: "GET", URL: "https://example.test/search?q=two", BodyCaptured: true},
		{Method: "GET", URL: "https://example.test/search?q=two", BodyCaptured: true},
	}
	result, err := FilterBatch(requests, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accepted) != 3 {
		t.Fatalf("len(Accepted) = %d, want 3", len(result.Accepted))
	}
	if result.Decisions[0].Action != ActionAllow || result.Decisions[0].Reason != ReasonNewRequest || result.Decisions[1].Action != ActionAllow {
		t.Fatalf("parameterized decisions = %#v, want distinct requests allowed", result.Decisions[:2])
	}
	if decision := result.Decisions[2]; decision.Action != ActionAllow || decision.Reason != ReasonNewRequest {
		t.Fatalf("repeated decision = %#v, want independent parameter strategy allow", decision)
	}
}

func TestRequireParametersAllowsIncompleteParsingWithoutHashFallback(t *testing.T) {
	session, err := NewSession(Options{DedupStrategy: DedupStrategyRequireParameters})
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
	decision, token, err = session.Reserve(request)
	if err != nil || decision.Action != ActionAllow || decision.ParameterStatus != ParameterStatusPartial {
		t.Fatalf("Reserve(repeated malformed) = (%#v, %v), want partial allow", decision, err)
	}
	if err := session.Commit(token); err != nil {
		t.Fatal(err)
	}
}
