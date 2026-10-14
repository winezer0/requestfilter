package requestfilter

import (
	"net/http"
	"reflect"
	"testing"
)

func FuzzExtractParameters(f *testing.F) {
	seeds := []struct {
		url, contentType, body string
	}{
		{"https://example.test/items?id=1", "", ""},
		{"https://example.test/items?id=%ZZ", "", ""},
		{"https://example.test/items", "application/json", `{"user":{"id":1}}`},
		{"https://example.test/items", "application/json", `[{"id":1},null,{}]`},
		{"https://example.test/items", "application/json", `{"id":1,"id":2}`},
		{"https://example.test/items", "application/problem+json; charset=utf-8", `{"a/b":{"~id":true}}`},
		{"https://example.test/items", "application/xml", `<root><item id="1"/><item id="2"/></root>`},
		{"https://example.test/items", "application/xml", `<root><broken></root>`},
		{"https://example.test/items", "application/x-www-form-urlencoded", "id=one&id=two&blank="},
		{"https://example.test/items", "application/x-www-form-urlencoded", "id=%ZZ"},
		{"https://example.test/items", "multipart/form-data; boundary=fuzz", "--fuzz--\r\n"},
		{"https://example.test/items", "multipart/form-data; boundary=fuzz", "--fuzz\r\nContent-Disposition: form-data; name=\"x\"\r\n\r\nvalue"},
		{"https://example.test/items", "application/octet-stream", "\x00\x01"},
	}
	for _, seed := range seeds {
		f.Add(seed.url, seed.contentType, seed.body)
	}

	f.Fuzz(func(t *testing.T, rawURL, contentType, body string) {
		const maxURLBytes = 4096
		const maxBodyBytes = 16 << 10
		if len(rawURL) > maxURLBytes || len(contentType) > 1024 || len(body) > maxBodyBytes {
			return
		}
		options, err := normalizeOptions(Options{
			DedupStrategy: DedupStrategyCoverageParameters,
			MaxURLBytes:   maxURLBytes, MaxBodyBytes: maxBodyBytes, MaxParameters: 64,
			MaxJSONDepth: 12, MaxXMLDepth: 12, MaxXMLNodes: 128, MaxMultipartParts: 64,
		})
		if err != nil {
			t.Fatal(err)
		}
		request := Request{
			Method: "POST", URL: rawURL,
			Header: http.Header{"Content-Type": {contentType}},
			Body:   []byte(body), BodyCaptured: true,
		}
		normalized, err := normalizeRequest(request, options)
		if err != nil {
			return
		}
		parameters, status, err := extractParameters(normalized, options)
		if err != nil {
			t.Fatalf("extractParameters() error = %v", err)
		}
		if status != ParameterStatusComplete && status != ParameterStatusPartial {
			t.Fatalf("extractParameters() status = %q, want complete or partial", status)
		}
		again, secondStatus, err := extractParameters(normalized, options)
		if err != nil || status != secondStatus || !reflect.DeepEqual(parameters, again) {
			t.Fatalf("extractParameters() is not deterministic: first=(%#v,%q), second=(%#v,%q), err=%v",
				parameters, status, again, secondStatus, err)
		}
		for index := 1; index < len(parameters); index++ {
			previous, current := parameters[index-1], parameters[index]
			if previous.Location > current.Location || previous.Location == current.Location && previous.Name >= current.Name {
				t.Fatalf("parameters are not unique and sorted: %#v", parameters)
			}
		}
	})
}

func FuzzNormalizeURL(f *testing.F) {
	for _, seed := range []string{
		"https://example.test/",
		"HTTP://EXAMPLE.TEST:80/path?q=1#fragment",
		"https://user:password@example.test/",
		"https://[::1]:443/path",
		"http://example.test/%2f/%2F?x=1&x=2",
		"*", "example.test:443", "https://example.test/%ZZ",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 4096 {
			return
		}
		first, firstPath, firstErr := normalizeURL(raw)
		second, secondPath, secondErr := normalizeURL(raw)
		if (firstErr == nil) != (secondErr == nil) || first != second || firstPath != secondPath {
			t.Fatalf("normalizeURL(%q) is not deterministic", raw)
		}
		if firstErr == nil && (first == "" || firstPath == "") {
			t.Fatalf("normalizeURL(%q) succeeded with empty normalized output", raw)
		}
	})
}
