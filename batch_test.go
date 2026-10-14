package requestfilter

import (
	"errors"
	"net/http"
	"testing"
)

func TestFilterBatchRequestIdentity(t *testing.T) {
	tests := []struct {
		name      string
		requests  []Request
		options   Options
		wantCount int
		wantLast  Reason
	}{
		{
			name:    "canonical host default port and fragment",
			options: Options{DedupStrategy: DedupStrategySelectedFieldsHash},
			requests: []Request{
				{URL: "HTTP://Example.COM:80/a#first"},
				{URL: "http://example.com/a#second"},
			},
			wantCount: 1, wantLast: ReasonExactDuplicate,
		},
		{
			name: "no strategy does not filter duplicate requests",
			requests: []Request{
				{URL: "https://example.test/a"},
				{URL: "https://example.test/a"},
			},
			wantCount: 2, wantLast: ReasonNewRequest,
		},
		{
			name:    "query order is preserved",
			options: Options{DedupStrategy: DedupStrategySelectedFieldsHash},
			requests: []Request{
				{URL: "https://example.test/?a=1&b=2"},
				{URL: "https://example.test/?b=2&a=1"},
			},
			wantCount: 2, wantLast: ReasonNewRequest,
		},
		{
			name:    "method is part of the fingerprint",
			options: Options{DedupStrategy: DedupStrategySelectedFieldsHash},
			requests: []Request{
				{Method: "GET", URL: "https://example.test/a"},
				{Method: "post", URL: "https://example.test/a"},
			},
			wantCount: 2, wantLast: ReasonNewRequest,
		},
		{
			name:    "authentication group partitions requests",
			options: Options{DedupStrategy: DedupStrategySelectedFieldsHash},
			requests: []Request{
				{URL: "https://example.test/a", AuthenticationGroupID: "user-a"},
				{URL: "https://example.test/a", AuthenticationGroupID: "user-b"},
			},
			wantCount: 2, wantLast: ReasonNewRequest,
		},
		{
			name:    "captured empty body differs from unknown body",
			options: Options{DedupStrategy: DedupStrategySelectedFieldsHash},
			requests: []Request{
				{Method: "POST", URL: "https://example.test/a"},
				{Method: "POST", URL: "https://example.test/a", BodyCaptured: true},
			},
			wantCount: 2, wantLast: ReasonNewRequest,
		},
		{
			name:    "body bytes are part of the fingerprint",
			options: Options{DedupStrategy: DedupStrategySelectedFieldsHash},
			requests: []Request{
				{Method: "POST", URL: "https://example.test/a", BodyCaptured: true, Body: []byte("x=1")},
				{Method: "POST", URL: "https://example.test/a", BodyCaptured: true, Body: []byte("x=2")},
			},
			wantCount: 2, wantLast: ReasonNewRequest,
		},
		{
			name:    "additional headers are normalized and included",
			options: Options{DedupStrategy: DedupStrategySelectedFieldsHash, AdditionalHashHeaderNames: []string{"x-mode"}},
			requests: []Request{
				{URL: "https://example.test/a", Header: http.Header{"X-Mode": {"one"}}},
				{URL: "https://example.test/a", Header: http.Header{"x-mode": {"two"}}},
			},
			wantCount: 2, wantLast: ReasonNewRequest,
		},
		{
			name:    "additional headers do not affect identity",
			options: Options{DedupStrategy: DedupStrategySelectedFieldsHash},
			requests: []Request{
				{URL: "https://example.test/a", Header: http.Header{"X-Noise": {"one"}}},
				{URL: "https://example.test/a", Header: http.Header{"X-Noise": {"two"}}},
			},
			wantCount: 1, wantLast: ReasonExactDuplicate,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := FilterBatch(test.requests, test.options)
			if err != nil {
				t.Fatalf("FilterBatch() error = %v", err)
			}
			if got := len(result.Accepted); got != test.wantCount {
				t.Fatalf("accepted count = %d, want %d", got, test.wantCount)
			}
			if got := result.Decisions[len(result.Decisions)-1].Reason; got != test.wantLast {
				t.Fatalf("last reason = %q, want %q", got, test.wantLast)
			}
		})
	}
}

func TestFilterBatchHTTPMethods(t *testing.T) {
	tests := []struct {
		name      string
		methods   []string
		wantCount int
	}{
		{name: "default method equals GET", methods: []string{"", "GET"}, wantCount: 1},
		{name: "method is case insensitive", methods: []string{"post", "POST"}, wantCount: 1},
		{
			name: "standard methods have distinct identities",
			methods: []string{
				"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE", "CONNECT",
			},
			wantCount: 9,
		},
		{name: "extension method is accepted", methods: []string{"PURGE", "PURGE"}, wantCount: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requests := make([]Request, 0, len(test.methods))
			for _, method := range test.methods {
				requests = append(requests, Request{
					Method: method, URL: "https://example.test/resource",
					BodyCaptured: true, Body: []byte("same body"),
				})
			}
			result, err := FilterBatch(requests, Options{DedupStrategy: DedupStrategySelectedFieldsHash})
			if err != nil {
				t.Fatalf("FilterBatch() error = %v", err)
			}
			if got := len(result.Accepted); got != test.wantCount {
				t.Fatalf("accepted count = %d, want %d", got, test.wantCount)
			}
		})
	}
}

func TestFilterBatchInvalidInputIsAtomic(t *testing.T) {
	requests := []Request{
		{URL: "https://example.test/valid"},
		{URL: "file:///etc/passwd"},
	}
	result, err := FilterBatch(requests, Options{})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("FilterBatch() error = %v, want ErrInvalidRequest", err)
	}
	if len(result.Accepted) != 0 || len(result.Decisions) != 0 {
		t.Fatalf("FilterBatch() returned partial result %#v", result)
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		input        string
		wantURL      string
		wantEndpoint string
		wantError    bool
	}{
		{input: "HTTPS://Example.COM:443/a%2Fb?q=1#frag", wantURL: "https://example.com/a%2Fb?q=1", wantEndpoint: "https://example.com/a%2Fb"},
		{input: "http://[2001:db8::1]:8080", wantURL: "http://[2001:db8::1]:8080/", wantEndpoint: "http://[2001:db8::1]:8080/"},
		{input: "https://example.test:0/", wantError: true},
		{input: "https://user:pass@example.test/", wantError: true},
		{input: "ftp://example.test/", wantError: true},
		{input: "https:///missing-host", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			gotURL, gotEndpoint, err := normalizeURL(test.input)
			if (err != nil) != test.wantError {
				t.Fatalf("normalizeURL() error = %v, wantError %t", err, test.wantError)
			}
			if err != nil {
				return
			}
			if gotURL != test.wantURL || gotEndpoint != test.wantEndpoint {
				t.Fatalf("normalizeURL() = (%q, %q), want (%q, %q)", gotURL, gotEndpoint, test.wantURL, test.wantEndpoint)
			}
		})
	}
}

func TestNewSessionRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name    string
		options Options
	}{
		{name: "unknown dedup strategy", options: Options{DedupStrategy: DedupStrategy(99)}},
		{name: "negative entry ttl", options: Options{EntryTTL: -1}},
		{name: "invalid additional hash header", options: Options{AdditionalHashHeaderNames: []string{"bad header"}}},
		{name: "zero max entries", options: Options{MaxEntries: -1}},
		{name: "negative XML depth", options: Options{MaxXMLDepth: -1}},
		{name: "negative XML node limit", options: Options{MaxXMLNodes: -1}},
		{name: "negative multipart part limit", options: Options{MaxMultipartParts: -1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewSession(test.options); !errors.Is(err, ErrInvalidOptions) {
				t.Fatalf("NewSession() error = %v, want ErrInvalidOptions", err)
			}
		})
	}
}
