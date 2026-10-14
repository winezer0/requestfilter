package requestfilter

import (
	"net/http"
	"testing"
)

func TestNormalizeBodyParserLimits(t *testing.T) {
	options, err := normalizeOptions(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if options.MaxXMLDepth != 64 || options.MaxXMLNodes != 10000 || options.MaxMultipartParts != 4096 {
		t.Fatalf("body parser defaults = (%d, %d, %d), want (64, 10000, 4096)",
			options.MaxXMLDepth, options.MaxXMLNodes, options.MaxMultipartParts)
	}
	tests := []struct {
		name    string
		options Options
	}{
		{name: "zero XML depth", options: Options{MaxXMLDepth: -1}},
		{name: "zero XML node limit", options: Options{MaxXMLNodes: -1}},
		{name: "zero multipart part limit", options: Options{MaxMultipartParts: -1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewSession(test.options); err != ErrInvalidOptions {
				t.Fatalf("NewSession() error = %v, want ErrInvalidOptions", err)
			}
		})
	}
}

func TestMultipartEmptyFilenameRemainsAFileField(t *testing.T) {
	request := Request{
		URL:          "https://example.test/upload",
		Header:       http.Header{"Content-Type": {"multipart/form-data; boundary=empty-file"}},
		BodyCaptured: true,
		Body:         []byte("--empty-file\r\nContent-Disposition: form-data; name=\"upload\"; filename=\"\"\r\nContent-Type: application/octet-stream\r\n\r\n\r\n--empty-file--\r\n"),
	}
	options, err := normalizeOptions(Options{DedupStrategy: DedupStrategyCoverageParameters})
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := normalizeRequest(request, options)
	if err != nil {
		t.Fatal(err)
	}
	parameters, status, err := extractParameters(normalized, options)
	if err != nil {
		t.Fatal(err)
	}
	if status != ParameterStatusComplete {
		t.Fatalf("status = %q, want complete", status)
	}
	want := []Parameter{{Location: ParameterMultipartFile, Name: "upload"}}
	if len(parameters) != len(want) || parameters[0] != want[0] {
		t.Fatalf("parameters = %#v, want %#v", parameters, want)
	}
}

func TestBodyParserLimitsAllowIncompleteRequestsWithoutStrategyFallback(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		options     Options
		body        string
	}{
		{
			name: "JSON depth", contentType: "application/json",
			options: Options{MaxJSONDepth: 1}, body: `{"one":{"two":1}}`,
		},
		{
			name: "XML depth", contentType: "application/xml",
			options: Options{MaxXMLDepth: 2}, body: `<root><item><id>1</id></item></root>`,
		},
		{
			name: "XML node count", contentType: "application/xml",
			options: Options{MaxXMLNodes: 2}, body: `<root><one/><two/></root>`,
		},
		{
			name: "multipart part count", contentType: "multipart/form-data; boundary=bounded",
			options: Options{MaxMultipartParts: 1},
			body:    "--bounded\r\nContent-Disposition: form-data; name=\"one\"\r\n\r\na\r\n--bounded\r\nContent-Disposition: form-data; name=\"two\"\r\n\r\nb\r\n--bounded--\r\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session, err := NewSession(Options{
				DedupStrategy: DedupStrategyCoverageParameters,
				MaxJSONDepth:  test.options.MaxJSONDepth,
				MaxXMLDepth:   test.options.MaxXMLDepth, MaxXMLNodes: test.options.MaxXMLNodes,
				MaxMultipartParts: test.options.MaxMultipartParts,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			request := Request{
				Method: "POST", URL: "https://example.test/data", Header: http.Header{"Content-Type": {test.contentType}},
				BodyCaptured: true, Body: []byte(test.body),
			}
			decision, token, err := session.Reserve(request)
			if err != nil || decision.Action != ActionAllow || decision.ParameterStatus != ParameterStatusPartial {
				t.Fatalf("Reserve() = (%#v, %v), want partial allow", decision, err)
			}
			if err := session.Commit(token); err != nil {
				t.Fatal(err)
			}
			request.Body = append([]byte(nil), request.Body...)
			request.Body[len(request.Body)-2] ^= 1
			decision, _, err = session.Reserve(request)
			if err != nil || decision.Action != ActionAllow || decision.ParameterStatus != ParameterStatusPartial {
				t.Fatalf("Reserve(different bytes) = (%#v, %v), want partial allow", decision, err)
			}
		})
	}
}

func TestHardRequestBodyLimitRemainsInvalid(t *testing.T) {
	session, err := NewSession(Options{DedupStrategy: DedupStrategyCoverageParameters, MaxBodyBytes: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	_, _, err = session.Reserve(Request{
		Method: "POST", URL: "https://example.test/data",
		Header:       http.Header{"Content-Type": {"application/json"}},
		BodyCaptured: true, Body: []byte("{} "),
	})
	if err != ErrInvalidRequest {
		t.Fatalf("Reserve() error = %v, want ErrInvalidRequest", err)
	}
}
