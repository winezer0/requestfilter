package requestfilter

import (
	"net/http"
	"testing"
)

func TestParameterExtractionEdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		request    Request
		wantStatus ParameterStatus
		want       []Parameter
	}{
		{
			name:       "repeated encoded blank and flag query parameters",
			request:    Request{URL: "https://example.test/?id=1&%69d=&=x&flag", BodyCaptured: true},
			wantStatus: ParameterStatusComplete,
			want: []Parameter{
				{Location: ParameterQuery, Name: ""},
				{Location: ParameterQuery, Name: "flag"},
				{Location: ParameterQuery, Name: "id"},
			},
		},
		{
			name:       "query parameter names are case sensitive and decoded as UTF-8",
			request:    Request{URL: "https://example.test/?ID=1&id=2&na%C3%AFve=3&plus+name=1&literal%2Bplus=2", BodyCaptured: true},
			wantStatus: ParameterStatusComplete,
			want: []Parameter{
				{Location: ParameterQuery, Name: "ID"},
				{Location: ParameterQuery, Name: "id"},
				{Location: ParameterQuery, Name: "literal+plus"},
				{Location: ParameterQuery, Name: "naïve"},
				{Location: ParameterQuery, Name: "plus name"},
			},
		},
		{
			name:       "malformed query escape marks the request partial without partial names",
			request:    Request{URL: "https://example.test/?good=1&bad=%ZZ", BodyCaptured: true},
			wantStatus: ParameterStatusPartial,
		},
		{
			name: "repeated blank and flag form parameters",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}},
				BodyCaptured: true, Body: []byte("id=1&id=2&empty=&=x&flag"),
			},
			wantStatus: ParameterStatusComplete,
			want: []Parameter{
				{Location: ParameterForm, Name: ""},
				{Location: ParameterForm, Name: "empty"},
				{Location: ParameterForm, Name: "flag"},
				{Location: ParameterForm, Name: "id"},
			},
		},
		{
			name: "malformed form escape is partial rather than partially trusted",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}},
				BodyCaptured: true, Body: []byte("good=1&bad=%ZZ"),
			},
			wantStatus: ParameterStatusPartial,
		},
		{
			name: "top-level JSON scalar",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/json"}},
				BodyCaptured: true, Body: []byte(`null`),
			},
			wantStatus: ParameterStatusComplete,
			want:       []Parameter{{Location: ParameterJSON, Name: "$"}},
		},
		{
			name: "top-level JSON array with objects null and empty containers",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/json"}},
				BodyCaptured: true, Body: []byte(`[{"id":1},{"id":2},null,{},[]]`),
			},
			wantStatus: ParameterStatusComplete,
			want: []Parameter{
				{Location: ParameterJSON, Name: "/0/id"},
				{Location: ParameterJSON, Name: "/1/id"},
				{Location: ParameterJSON, Name: "/2"},
				{Location: ParameterJSON, Name: "/3"},
				{Location: ParameterJSON, Name: "/4"},
			},
		},
		{
			name: "duplicate JSON object keys collapse to one name",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/json"}},
				BodyCaptured: true, Body: []byte(`{"id":1,"id":2}`),
			},
			wantStatus: ParameterStatusComplete,
			want:       []Parameter{{Location: ParameterJSON, Name: "/id"}},
		},
		{
			name: "JSON names preserve case and UTF-8",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/json"}},
				BodyCaptured: true, Body: []byte(`{"ID":1,"id":2,"naïve":3}`),
			},
			wantStatus: ParameterStatusComplete,
			want: []Parameter{
				{Location: ParameterJSON, Name: "/ID"},
				{Location: ParameterJSON, Name: "/id"},
				{Location: ParameterJSON, Name: "/naïve"},
			},
		},
		{
			name: "vendor JSON media type with charset parameter",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {"Application/Vnd.Example+Json; charset=utf-8"}},
				BodyCaptured: true, Body: []byte(`{"id":1}`),
			},
			wantStatus: ParameterStatusComplete,
			want:       []Parameter{{Location: ParameterJSON, Name: "/id"}},
		},
		{
			name: "non-empty body without content type is partial",
			request: Request{
				URL: "https://example.test/", BodyCaptured: true, Body: []byte(`{"id":1}`),
			},
			wantStatus: ParameterStatusPartial,
		},
		{
			name: "text body without a recognized parser is partial",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
				BodyCaptured: true, Body: []byte("id=1"),
			},
			wantStatus: ParameterStatusPartial,
		},
		{
			name: "XML with a non UTF-8 declaration is partial",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {"text/xml; charset=iso-8859-1"}},
				BodyCaptured: true, Body: []byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><root>caf\xe9</root>"),
			},
			wantStatus: ParameterStatusPartial,
		},
		{
			name: "empty body without content type is complete empty shape",
			request: Request{
				URL: "https://example.test/", BodyCaptured: true,
			},
			wantStatus: ParameterStatusComplete,
		},
		{
			name: "empty multipart body without a closing boundary is partial",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {"multipart/form-data; boundary=empty"}},
				BodyCaptured: true,
			},
			wantStatus: ParameterStatusPartial,
		},
		{
			name: "multipart with only a closing boundary is complete empty shape",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {"multipart/form-data; boundary=empty"}},
				BodyCaptured: true, Body: []byte("--empty--\r\n"),
			},
			wantStatus: ParameterStatusComplete,
		},
		{
			name: "uncaptured empty body is partial",
			request: Request{
				URL: "https://example.test/",
			},
			wantStatus: ParameterStatusPartial,
		},
	}
	options, err := normalizeOptions(Options{DedupStrategy: DedupStrategyCoverageParameters})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			normalized, err := normalizeRequest(test.request, options)
			if err != nil {
				t.Fatalf("normalizeRequest() error = %v", err)
			}
			got, status, err := extractParameters(normalized, options)
			if err != nil {
				t.Fatalf("extractParameters() error = %v", err)
			}
			if status != test.wantStatus {
				t.Fatalf("status = %q, want %q", status, test.wantStatus)
			}
			if len(got) != len(test.want) {
				t.Fatalf("parameters = %#v, want %#v", got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("parameters = %#v, want %#v", got, test.want)
				}
			}
		})
	}
}

func TestDedupStrategyCoverageParametersContentTypeAndMultipartBoundaryVariants(t *testing.T) {
	tests := []struct {
		name   string
		first  Request
		second Request
	}{
		{
			name: "JSON media type parameters do not split coverage",
			first: Request{
				Method: "POST", URL: "https://example.test/data",
				Header:       http.Header{"Content-Type": {"application/json; charset=utf-8"}},
				BodyCaptured: true, Body: []byte(`{"id":1}`),
			},
			second: Request{
				Method: "POST", URL: "https://example.test/data",
				Header:       http.Header{"Content-Type": {"Application/JSON; charset=utf-16"}},
				BodyCaptured: true, Body: []byte(`{"id":2}`),
			},
		},
		{
			name: "multipart boundary does not split coverage",
			first: Request{
				Method: "POST", URL: "https://example.test/upload",
				Header:       http.Header{"Content-Type": {"multipart/form-data; boundary=first"}},
				BodyCaptured: true, Body: []byte("--first\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.txt\"\r\n\r\na\r\n--first--\r\n"),
			},
			second: Request{
				Method: "POST", URL: "https://example.test/upload",
				Header:       http.Header{"Content-Type": {"multipart/form-data; boundary=\"second\""}},
				BodyCaptured: true, Body: []byte("--second\r\nContent-Disposition: form-data; name=\"file\"; filename=\"b.txt\"\r\n\r\nb\r\n--second--\r\n"),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session, err := NewSession(Options{DedupStrategy: DedupStrategyCoverageParameters})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			first, token, err := session.Reserve(test.first)
			if err != nil || first.Action != ActionAllow || first.ParameterStatus != ParameterStatusComplete {
				t.Fatalf("Reserve(first) = (%#v, %v), want complete allow", first, err)
			}
			if err := session.Commit(token); err != nil {
				t.Fatal(err)
			}
			second, _, err := session.Reserve(test.second)
			if err != nil || second.Action != ActionDrop || second.Reason != ReasonParameterCoverage {
				t.Fatalf("Reserve(second) = (%#v, %v), want same-shape drop", second, err)
			}
		})
	}
}

func TestMultipartRepeatedFieldsFilesAndExtendedFilename(t *testing.T) {
	body := "--multi\r\nContent-Disposition: form-data; name=\"field\"\r\n\r\none\r\n" +
		"--multi\r\nContent-Disposition: form-data; name=\"field\"\r\n\r\ntwo\r\n" +
		"--multi\r\nContent-Disposition: form-data; name=\"upload\"; filename*=utf-8''first.txt\r\n\r\nfirst\r\n" +
		"--multi\r\nContent-Disposition: form-data; name=\"upload\"; filename=\"second.txt\"\r\n\r\nsecond\r\n--multi--\r\n"
	options, err := normalizeOptions(Options{DedupStrategy: DedupStrategyCoverageParameters})
	if err != nil {
		t.Fatal(err)
	}
	request, err := normalizeRequest(Request{
		URL: "https://example.test/upload", Header: http.Header{"Content-Type": {"multipart/form-data; boundary=multi"}},
		BodyCaptured: true, Body: []byte(body),
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	parameters, status, err := extractParameters(request, options)
	if err != nil || status != ParameterStatusComplete {
		t.Fatalf("extractParameters() = (%#v, %q, %v), want complete", parameters, status, err)
	}
	want := []Parameter{
		{Location: ParameterMultipartField, Name: "field"},
		{Location: ParameterMultipartFile, Name: "upload"},
	}
	if len(parameters) != len(want) || parameters[0] != want[0] || parameters[1] != want[1] {
		t.Fatalf("parameters = %#v, want %#v", parameters, want)
	}
}

func TestDedupStrategyCoverageParametersArrayPositionAndDuplicateKeySemantics(t *testing.T) {
	session, err := NewSession(Options{DedupStrategy: DedupStrategyCoverageParameters})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	request := func(body string) Request {
		return Request{
			Method: "POST", URL: "https://example.test/data",
			Header:       http.Header{"Content-Type": {"application/json"}},
			BodyCaptured: true, Body: []byte(body),
		}
	}
	first, token, err := session.Reserve(request(`{"id":1,"id":2}`))
	if err != nil || first.Action != ActionAllow {
		t.Fatalf("Reserve(first duplicate-key JSON) = (%#v, %v), want allow", first, err)
	}
	if err := session.Commit(token); err != nil {
		t.Fatal(err)
	}
	duplicateKeyShape, _, err := session.Reserve(request(`{"id":3}`))
	if err != nil || duplicateKeyShape.Action != ActionDrop {
		t.Fatalf("Reserve(same object key shape) = (%#v, %v), want drop", duplicateKeyShape, err)
	}
	newArrayPosition, token, err := session.Reserve(request(`[{"id":4},{"name":"x"}]`))
	if err != nil || newArrayPosition.Action != ActionAllow {
		t.Fatalf("Reserve(new array position shape) = (%#v, %v), want allow", newArrayPosition, err)
	}
	if err := session.Commit(token); err != nil {
		t.Fatal(err)
	}
	reordered, _, err := session.Reserve(request(`[{"name":"x"},{"id":4}]`))
	if err != nil || reordered.Action != ActionAllow {
		t.Fatalf("Reserve(reordered array shape) = (%#v, %v), want new indexed shape allow", reordered, err)
	}
}

func TestBodyCaptureAndParserLimitBoundaries(t *testing.T) {
	t.Run("body limit accepts exact size and rejects one byte over", func(t *testing.T) {
		session, err := NewSession(Options{DedupStrategy: DedupStrategyCoverageParameters, MaxBodyBytes: 2})
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		request := Request{
			Method: "POST", URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/json"}},
			BodyCaptured: true, Body: []byte(`{}`),
		}
		decision, token, err := session.Reserve(request)
		if err != nil || decision.Action != ActionAllow || decision.ParameterStatus != ParameterStatusComplete {
			t.Fatalf("Reserve(exact limit) = (%#v, %v), want complete allow", decision, err)
		}
		if err := session.Commit(token); err != nil {
			t.Fatal(err)
		}
		request.Body = []byte(`{} `)
		if _, _, err := session.Reserve(request); err != ErrInvalidRequest {
			t.Fatalf("Reserve(over limit) error = %v, want ErrInvalidRequest", err)
		}
	})

	t.Run("parameter limit accepts exact count and marks one over partial", func(t *testing.T) {
		options, err := normalizeOptions(Options{DedupStrategy: DedupStrategyCoverageParameters, MaxParameters: 2})
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			query string
			want  ParameterStatus
		}{
			{query: "?a=1&b=2", want: ParameterStatusComplete},
			{query: "?a=1&b=2&c=3", want: ParameterStatusPartial},
		} {
			request, err := normalizeRequest(Request{URL: "https://example.test/" + test.query, BodyCaptured: true}, options)
			if err != nil {
				t.Fatal(err)
			}
			_, status, err := extractParameters(request, options)
			if err != nil || status != test.want {
				t.Fatalf("extractParameters(%s) = (%q, %v), want %q", test.query, status, err, test.want)
			}
		}
	})

	t.Run("JSON depth exact limit and one over", func(t *testing.T) {
		for _, test := range []struct {
			name string
			body string
			want ParameterStatus
		}{
			{name: "exact", body: `{"a":1}`, want: ParameterStatusComplete},
			{name: "over", body: `{"a":{"b":1}}`, want: ParameterStatusPartial},
		} {
			t.Run(test.name, func(t *testing.T) {
				options, err := normalizeOptions(Options{DedupStrategy: DedupStrategyCoverageParameters, MaxJSONDepth: 1})
				if err != nil {
					t.Fatal(err)
				}
				request, err := normalizeRequest(Request{
					URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/json"}},
					BodyCaptured: true, Body: []byte(test.body),
				}, options)
				if err != nil {
					t.Fatal(err)
				}
				_, status, err := extractParameters(request, options)
				if err != nil || status != test.want {
					t.Fatalf("extractParameters() = (%q, %v), want %q", status, err, test.want)
				}
			})
		}
	})

	t.Run("multipart part limit accepts exact count", func(t *testing.T) {
		options, err := normalizeOptions(Options{DedupStrategy: DedupStrategyCoverageParameters, MaxMultipartParts: 1})
		if err != nil {
			t.Fatal(err)
		}
		request, err := normalizeRequest(Request{
			URL: "https://example.test/", Header: http.Header{"Content-Type": {"multipart/form-data; boundary=one"}},
			BodyCaptured: true,
			Body:         []byte("--one\r\nContent-Disposition: form-data; name=\"field\"\r\n\r\nvalue\r\n--one--\r\n"),
		}, options)
		if err != nil {
			t.Fatal(err)
		}
		_, status, err := extractParameters(request, options)
		if err != nil || status != ParameterStatusComplete {
			t.Fatalf("extractParameters() = (%q, %v), want complete", status, err)
		}
	})

	t.Run("XML depth and node limits accept exact bounds", func(t *testing.T) {
		for _, test := range []struct {
			name    string
			options Options
			body    string
		}{
			{name: "depth", options: Options{MaxXMLDepth: 2}, body: `<root><id/></root>`},
			{name: "nodes include attributes", options: Options{MaxXMLNodes: 2}, body: `<root id="1"/>`},
		} {
			t.Run(test.name, func(t *testing.T) {
				options, err := normalizeOptions(Options{DedupStrategy: DedupStrategyCoverageParameters,
					MaxXMLDepth: test.options.MaxXMLDepth, MaxXMLNodes: test.options.MaxXMLNodes})
				if err != nil {
					t.Fatal(err)
				}
				request, err := normalizeRequest(Request{
					URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/xml"}},
					BodyCaptured: true, Body: []byte(test.body),
				}, options)
				if err != nil {
					t.Fatal(err)
				}
				_, status, err := extractParameters(request, options)
				if err != nil || status != ParameterStatusComplete {
					t.Fatalf("extractParameters() = (%q, %v), want complete", status, err)
				}
			})
		}
	})
}

func TestAuthenticationRepeatedHeaderValuesRemainDistinctAndStable(t *testing.T) {
	for _, test := range []struct {
		name   string
		header string
		values []string
	}{
		{name: "Authorization", header: "Authorization", values: []string{"Bearer one", "Bearer two"}},
		{name: "token header", header: "X-Auth-Token", values: []string{"token-one", "token-two"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, err := NewSession(Options{DedupStrategy: DedupStrategySelectedFieldsHash})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			request := func(values ...string) Request {
				return Request{
					Method: "GET", URL: "https://example.test/", BodyCaptured: true,
					Header: http.Header{test.header: values},
				}
			}
			first, token, err := session.Reserve(request(test.values...))
			if err != nil || first.Action != ActionAllow {
				t.Fatalf("Reserve(first repeated header) = (%#v, %v), want allow", first, err)
			}
			if err := session.Commit(token); err != nil {
				t.Fatal(err)
			}
			duplicate, _, err := session.Reserve(request(test.values...))
			if err != nil || duplicate.Action != ActionDrop {
				t.Fatalf("Reserve(identical repeated headers) = (%#v, %v), want exact duplicate drop", duplicate, err)
			}
			reversed := []string{test.values[1], test.values[0]}
			reordered, token, err := session.Reserve(request(reversed...))
			if err != nil || reordered.Action != ActionAllow {
				t.Fatalf("Reserve(reordered repeated headers) = (%#v, %v), want distinct identity allow", reordered, err)
			}
			if err := session.Commit(token); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCookieAuthenticationAcrossRepeatedHeaderLines(t *testing.T) {
	session, err := NewSession(Options{DedupStrategy: DedupStrategySelectedFieldsHash})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	request := func(values ...string) Request {
		return Request{
			Method: "GET", URL: "https://example.test/", BodyCaptured: true,
			Header: http.Header{"Cookie": values},
		}
	}
	first, token, err := session.Reserve(request("a=1; a=2; b=2", "c=3"))
	if err != nil || first.Action != ActionAllow {
		t.Fatalf("Reserve(first cookie lines) = (%#v, %v), want allow", first, err)
	}
	if err := session.Commit(token); err != nil {
		t.Fatal(err)
	}
	reordered, _, err := session.Reserve(request("c=3; a=2", "b=2; a=1"))
	if err != nil || reordered.Action != ActionDrop {
		t.Fatalf("Reserve(reordered cookie lines) = (%#v, %v), want same authentication identity", reordered, err)
	}
}

func TestRawHTTPRequestTargetsNeedCallerMapping(t *testing.T) {
	for _, test := range []struct {
		method string
		target string
	}{
		{method: "CONNECT", target: "example.test:443"},
		{method: "OPTIONS", target: "*"},
	} {
		t.Run(test.method+" "+test.target, func(t *testing.T) {
			_, err := FilterBatch([]Request{{Method: test.method, URL: test.target, BodyCaptured: true}}, Options{})
			if err != ErrInvalidRequest {
				t.Fatalf("FilterBatch(raw request target) error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestEmptyMalformedMultipartBypassesParameterStrategies(t *testing.T) {
	for _, test := range []struct {
		name     string
		strategy DedupStrategy
	}{
		{name: "DedupStrategyRequireParameters", strategy: DedupStrategyRequireParameters},
		{name: "DedupStrategyCoverageParameters", strategy: DedupStrategyCoverageParameters},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, err := NewSession(Options{DedupStrategy: test.strategy})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			request := Request{
				Method: "POST", URL: "https://example.test/upload",
				Header:       http.Header{"Content-Type": {"multipart/form-data; boundary=empty"}},
				BodyCaptured: true,
			}
			for attempt := 0; attempt < 2; attempt++ {
				decision, reservation, err := session.Reserve(request)
				if err != nil || decision.Action != ActionAllow || decision.ParameterStatus != ParameterStatusPartial {
					t.Fatalf("Reserve(attempt %d) = (%#v, %v), want partial allow", attempt, decision, err)
				}
				if err := session.Commit(reservation); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
