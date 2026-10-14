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
		{URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"Authorization": {"Basic dXNlcjpwYXNz=="}}},
		{URL: "https://example.test/", BodyCaptured: true, Header: http.Header{"authorization": {"basic   dXNlcjpwYXNz=="}}},
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

func TestAuthenticationSourcesAcrossAllStrategies(t *testing.T) {
	strategies := []DedupStrategy{
		DedupStrategySelectedFieldsHash,
		DedupStrategyRequireParameters,
		DedupStrategyParameterShapeCoverage,
	}
	sources := []struct {
		name         string
		header       string
		customHeader bool
		callerID     bool
	}{
		{name: "Cookie", header: "Cookie"},
		{name: "Authorization", header: "Authorization"},
		{name: "keyword header", header: "x-SeSsIoN-id"},
		{name: "custom header", header: "X-API-Key", customHeader: true},
		{name: "caller group ID", callerID: true},
	}

	for _, strategy := range strategies {
		for _, source := range sources {
			t.Run(string(strategy)+"/"+source.name, func(t *testing.T) {
				options := Options{DedupStrategy: strategy}
				if source.customHeader {
					options.AdditionalAuthenticationHeaderNames = []string{source.header}
				}
				session, err := NewSession(options)
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()

				makeRequest := func(identity, queryValue string) Request {
					url := "https://example.test/items"
					if strategy == DedupStrategySelectedFieldsHash {
						url += "?id=1"
					} else if strategy == DedupStrategyParameterShapeCoverage {
						url += "?id=" + queryValue
					}
					req := Request{Method: "GET", URL: url, BodyCaptured: true, Header: make(http.Header)}
					if source.callerID {
						req.AuthenticationGroupID = identity
					} else if source.header == "Cookie" {
						req.Header[source.header] = []string{"SESSION=" + identity}
					} else {
						req.Header[source.header] = []string{identity}
					}
					return req
				}
				reserve := func(request Request, want Action, commit bool) {
					t.Helper()
					decision, reservation, err := session.Reserve(request)
					if err != nil || decision.Action != want {
						t.Fatalf("Reserve() = (%#v, %v), want action %q", decision, err, want)
					}
					if commit {
						if err := session.Commit(reservation); err != nil {
							t.Fatal(err)
						}
					}
				}

				reserve(makeRequest("identity-a", "1"), ActionAllow, true)
				reserve(makeRequest("identity-a", "2"), ActionDrop, false)
				reserve(makeRequest("identity-b", "3"), ActionAllow, true)
				reserve(makeRequest("identity-b", "4"), ActionDrop, false)
			})
		}
	}
}

func TestCallerAuthenticationGroupCombinesWithDetectedHeaders(t *testing.T) {
	strategies := []DedupStrategy{
		DedupStrategySelectedFieldsHash,
		DedupStrategyRequireParameters,
		DedupStrategyParameterShapeCoverage,
	}
	for _, strategy := range strategies {
		t.Run(string(strategy), func(t *testing.T) {
			session, err := NewSession(Options{DedupStrategy: strategy})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()

			makeRequest := func(cookie, group, query string) Request {
				url := "https://example.test/items"
				if strategy == DedupStrategySelectedFieldsHash {
					url += "?id=1"
				} else if strategy == DedupStrategyParameterShapeCoverage {
					url += "?id=" + query
				}
				return Request{
					Method: "GET", URL: url, BodyCaptured: true,
					Header: http.Header{"Cookie": {"SESSION=" + cookie}}, AuthenticationGroupID: group,
				}
			}
			reserve := func(request Request, want Action, commit bool) {
				t.Helper()
				decision, reservation, err := session.Reserve(request)
				if err != nil || decision.Action != want {
					t.Fatalf("Reserve() = (%#v, %v), want action %q", decision, err, want)
				}
				if commit {
					if err := session.Commit(reservation); err != nil {
						t.Fatal(err)
					}
				}
			}

			reserve(makeRequest("cookie-a", "caller-a", "1"), ActionAllow, true)
			reserve(makeRequest("cookie-b", "caller-a", "2"), ActionAllow, true)
			reserve(makeRequest("cookie-b", "caller-b", "3"), ActionAllow, true)
			reserve(makeRequest("cookie-b", "caller-b", "4"), ActionDrop, false)
		})
	}
}

func TestProxyAuthorizationAloneDoesNotPartitionAnyStrategy(t *testing.T) {
	strategies := []DedupStrategy{
		DedupStrategySelectedFieldsHash,
		DedupStrategyRequireParameters,
		DedupStrategyParameterShapeCoverage,
	}
	for _, strategy := range strategies {
		t.Run(string(strategy), func(t *testing.T) {
			session, err := NewSession(Options{DedupStrategy: strategy})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()

			makeRequest := func(proxyCredential, query string) Request {
				url := "https://example.test/items"
				if strategy == DedupStrategySelectedFieldsHash {
					url += "?id=1"
				} else if strategy == DedupStrategyParameterShapeCoverage {
					url += "?id=" + query
				}
				return Request{
					Method: "GET", URL: url, BodyCaptured: true,
					Header: http.Header{"Proxy-Authorization": {proxyCredential}},
				}
			}
			first, reservation, err := session.Reserve(makeRequest("Basic proxy-one", "1"))
			if err != nil || first.Action != ActionAllow {
				t.Fatalf("Reserve(first) = (%#v, %v), want allow", first, err)
			}
			if err := session.Commit(reservation); err != nil {
				t.Fatal(err)
			}
			second, _, err := session.Reserve(makeRequest("Basic proxy-two", "2"))
			if err != nil || second.Action != ActionDrop {
				t.Fatalf("Reserve(second) = (%#v, %v), want proxy credentials not to partition", second, err)
			}
		})
	}
}
