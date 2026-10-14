package requestfilter

import (
	"net/http"
	"strings"
	"testing"
)

func TestBuiltInAuthenticationSourcesPartitionEveryStrategy(t *testing.T) {
	tests := []struct {
		name     string
		strategy DedupStrategy
		requests []Request
	}{
		{
			name:     "HASH Authorization header",
			strategy: DedupStrategySelectedFieldsHash,
			requests: []Request{
				{Method: "GET", URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"Authorization": {"Bearer one"}}},
				{Method: "GET", URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"Authorization": {"Bearer one"}}},
				{Method: "GET", URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"Authorization": {"Bearer two"}}},
				{Method: "GET", URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"Authorization": {"Bearer two"}}},
			},
		},
		{
			name:     "PARAM Cookie header",
			strategy: DedupStrategyRequireParameters,
			requests: []Request{
				{Method: "GET", URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"Cookie": {"visitor=one"}}},
				{Method: "GET", URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"Cookie": {"visitor=one"}}},
				{Method: "GET", URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"Cookie": {"visitor=two"}}},
				{Method: "GET", URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"Cookie": {"visitor=two"}}},
			},
		},
		{
			name:     "SMART token header",
			strategy: DedupStrategyParameterShapeCoverage,
			requests: []Request{
				{Method: "GET", URL: "https://example.test/?id=1", BodyCaptured: true, Header: http.Header{"X-Auth-Token": {"one"}}},
				{Method: "GET", URL: "https://example.test/?id=2", BodyCaptured: true, Header: http.Header{"X-Auth-Token": {"one"}}},
				{Method: "GET", URL: "https://example.test/?id=3", BodyCaptured: true, Header: http.Header{"X-Auth-Token": {"two"}}},
				{Method: "GET", URL: "https://example.test/?id=4", BodyCaptured: true, Header: http.Header{"X-Auth-Token": {"two"}}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := FilterBatch(test.requests, Options{DedupStrategy: test.strategy})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Accepted) != 2 {
				t.Fatalf("len(Accepted) = %d, want 2", len(result.Accepted))
			}
			wantActions := []Action{ActionAllow, ActionDrop, ActionAllow, ActionDrop}
			for index, want := range wantActions {
				if got := result.Decisions[index].Action; got != want {
					t.Fatalf("decision[%d].Action = %q, want %q", index, got, want)
				}
			}
		})
	}
}

func TestCookieAuthenticationIdentityIgnoresPairOrder(t *testing.T) {
	requests := []Request{
		{URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"Cookie": {"theme=dark; SID=one"}}},
		{URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"Cookie": {"SID=one; theme=dark"}}},
		{URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"Cookie": {"SID=two; theme=dark"}}},
	}
	result, err := FilterBatch(requests, Options{DedupStrategy: DedupStrategySelectedFieldsHash})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accepted) != 2 || result.Decisions[1].Action != ActionDrop || result.Decisions[2].Action != ActionAllow {
		t.Fatalf("FilterBatch() = (%#v, %v), want reordered cookie duplicate dropped and changed cookie allowed", result, err)
	}
}

func TestAuthorizationAuthenticationIdentityIgnoresSchemeCase(t *testing.T) {
	requests := []Request{
		{URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"Authorization": {"Bearer token-value"}}},
		{URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"authorization": {"bearer token-value"}}},
	}
	result, err := FilterBatch(requests, Options{DedupStrategy: DedupStrategySelectedFieldsHash})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accepted) != 1 || result.Decisions[1].Action != ActionDrop {
		t.Fatalf("FilterBatch() = (%#v, %v), want auth scheme case variation treated as same identity", result, err)
	}
}

func TestAuthenticationHeaderNamesAndProxyExclusion(t *testing.T) {
	requests := []Request{
		{URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"X-API-Key": {"one"}, "Proxy-Authorization": {"Basic proxy-one"}, "X-Session-Id": {"session-one"}}},
		{URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"X-API-Key": {"two"}, "Proxy-Authorization": {"Basic proxy-two"}, "X-Session-Id": {"session-one"}}},
		{URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"X-API-Key": {"two"}, "Proxy-Authorization": {"Basic proxy-two"}, "X-Session-Id": {"session-two"}}},
	}

	defaultResult, err := FilterBatch(requests[:2], Options{DedupStrategy: DedupStrategySelectedFieldsHash})
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultResult.Accepted) != 1 {
		t.Fatalf("default header matching accepted %d requests, want 1; Proxy-Authorization and X-API-Key are not default identities", len(defaultResult.Accepted))
	}

	customResult, err := FilterBatch(requests[:2], Options{
		DedupStrategy: DedupStrategySelectedFieldsHash, AdditionalAuthenticationHeaderNames: []string{"x-api-key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(customResult.Accepted) != 2 {
		t.Fatalf("custom header accepted %d requests, want 2", len(customResult.Accepted))
	}

	result, err := FilterBatch([]Request{requests[0], requests[2]}, Options{DedupStrategy: DedupStrategySelectedFieldsHash})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accepted) != 2 {
		t.Fatalf("common auth header accepted %d requests, want distinct sessions", len(result.Accepted))
	}
}

func TestAuthenticationCredentialsAreNotStoredInSessionState(t *testing.T) {
	const secret = "credential-value-should-not-be-stored"
	session, err := NewSession(Options{DedupStrategy: DedupStrategySelectedFieldsHash})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	request := Request{
		URL:                   "https://example.test/",
		BodyCaptured:          true,
		AuthenticationGroupID: secret,
		Header: http.Header{
			"Authorization": {"Bearer " + secret},
			"Cookie":        {"session=" + secret},
			"X-Auth-Token":  {secret},
		},
	}
	decision, reservation, err := session.Reserve(request)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve() = (%#v, %v), want allow", decision, err)
	}
	if strings.Contains(decision.Fingerprint, secret) {
		t.Fatal("decision fingerprint contains an authentication value")
	}
	state := session.pending[reservation.id]
	if state == nil {
		t.Fatal("reservation state was not recorded")
	}
	if strings.Contains(state.hashKey, secret) || strings.Contains(state.endpointKey, secret) {
		t.Fatal("reservation state contains a raw authentication value")
	}
	if err := session.Commit(reservation); err != nil {
		t.Fatal(err)
	}
	for key := range session.exact {
		if strings.Contains(key, secret) {
			t.Fatal("committed dedup state contains a raw authentication value")
		}
	}
}
