package requestfilter

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"reflect"
	"testing"
)

func TestParameterExtractionLocations(t *testing.T) {
	var multipartBody bytes.Buffer
	multipartWriter := multipart.NewWriter(&multipartBody)
	if err := multipartWriter.WriteField("caption", "hello"); err != nil {
		t.Fatal(err)
	}
	filePart, err := multipartWriter.CreateFormFile("upload", "report.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := filePart.Write([]byte("file bytes are not extracted")); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		request    Request
		wantStatus ParameterStatus
		want       []Parameter
	}{
		{
			name: "query and nested JSON arrays",
			request: Request{
				Method: "POST", URL: "https://example.test/?q=1",
				Header:       http.Header{"Content-Type": {"application/json"}},
				BodyCaptured: true, Body: []byte(`{"a":[{"id":1},{"name":"n"}],"empty":{}}`),
			},
			wantStatus: ParameterStatusComplete,
			want: []Parameter{
				{Location: ParameterJSON, Name: "/a/0/id"},
				{Location: ParameterJSON, Name: "/a/1/name"},
				{Location: ParameterJSON, Name: "/empty"},
				{Location: ParameterQuery, Name: "q"},
			},
		},
		{
			name: "form body",
			request: Request{
				Method: "POST", URL: "https://example.test/",
				Header:       http.Header{"Content-Type": {"application/x-www-form-urlencoded; charset=utf-8"}},
				BodyCaptured: true, Body: []byte("name=alice&tag=one&tag=two"),
			},
			wantStatus: ParameterStatusComplete,
			want: []Parameter{
				{Location: ParameterForm, Name: "name"},
				{Location: ParameterForm, Name: "tag"},
			},
		},
		{
			name: "XML elements and attributes",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/xml"}},
				BodyCaptured: true, Body: []byte(`<user id="7"><name>Ada</name><empty/><item>one</item><item>two</item></user>`),
			},
			wantStatus: ParameterStatusComplete,
			want: []Parameter{
				{Location: ParameterXML, Name: "/user/@id"},
				{Location: ParameterXML, Name: "/user/empty"},
				{Location: ParameterXML, Name: "/user/item/0"},
				{Location: ParameterXML, Name: "/user/item/1"},
				{Location: ParameterXML, Name: "/user/name"},
			},
		},
		{
			name: "XML namespace is part of the path",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/problem+xml"}},
				BodyCaptured: true, Body: []byte(`<x:root xmlns:x="urn:example"><x:id>7</x:id></x:root>`),
			},
			wantStatus: ParameterStatusComplete,
			want: []Parameter{
				{Location: ParameterXML, Name: "/{urn:example}root/{urn:example}id"},
			},
		},
		{
			name: "multipart fields and upload names",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {multipartWriter.FormDataContentType()}},
				BodyCaptured: true, Body: multipartBody.Bytes(),
			},
			wantStatus: ParameterStatusComplete,
			want: []Parameter{
				{Location: ParameterMultipartField, Name: "caption"},
				{Location: ParameterMultipartFile, Name: "upload"},
			},
		},
		{
			name: "unknown body format is partial and keeps query only",
			request: Request{
				URL: "https://example.test/?q=1", Header: http.Header{"Content-Type": {"application/octet-stream"}},
				BodyCaptured: true, Body: []byte{0, 1, 2},
			},
			wantStatus: ParameterStatusPartial,
			want:       []Parameter{{Location: ParameterQuery, Name: "q"}},
		},
		{
			name: "malformed XML discards extracted body names",
			request: Request{
				URL: "https://example.test/?q=1", Header: http.Header{"Content-Type": {"application/xml"}},
				BodyCaptured: true, Body: []byte(`<root><id>1</root>`),
			},
			wantStatus: ParameterStatusPartial,
			want:       []Parameter{{Location: ParameterQuery, Name: "q"}},
		},
		{
			name: "invalid text outside XML root reports partial",
			request: Request{
				URL: "https://example.test/?q=1", Header: http.Header{"Content-Type": {"application/xml"}},
				BodyCaptured: true, Body: []byte(`text<root><id>1</id></root>`),
			},
			wantStatus: ParameterStatusPartial,
			want:       []Parameter{{Location: ParameterQuery, Name: "q"}},
		},
		{
			name: "truncated multipart discards extracted field names",
			request: Request{
				URL:          "https://example.test/?q=1",
				Header:       http.Header{"Content-Type": {"multipart/form-data; boundary=incomplete"}},
				BodyCaptured: true, Body: []byte("--incomplete\r\nContent-Disposition: form-data; name=\"field\"\r\n\r\nvalue"),
			},
			wantStatus: ParameterStatusPartial,
			want:       []Parameter{{Location: ParameterQuery, Name: "q"}},
		},
		{
			name: "JSON pointer escapes slash and tilde",
			request: Request{
				URL: "https://example.test/", Header: http.Header{"Content-Type": {"application/problem+json"}},
				BodyCaptured: true, Body: []byte(`{"a/b":{"~key":null}}`),
			},
			wantStatus: ParameterStatusComplete,
			want:       []Parameter{{Location: ParameterJSON, Name: "/a~1b/~0key"}},
		},
		{
			name: "malformed JSON reports partial",
			request: Request{
				URL: "https://example.test/?q=1", Header: http.Header{"Content-Type": {"application/json"}},
				BodyCaptured: true, Body: []byte(`{"id":`),
			},
			wantStatus: ParameterStatusPartial,
			want:       []Parameter{{Location: ParameterQuery, Name: "q"}},
		},
		{
			name: "trailing JSON value discards body parameters",
			request: Request{
				URL: "https://example.test/?q=1", Header: http.Header{"Content-Type": {"application/json"}},
				BodyCaptured: true, Body: []byte(`{"id":1} {"extra":2}`),
			},
			wantStatus: ParameterStatusPartial,
			want:       []Parameter{{Location: ParameterQuery, Name: "q"}},
		},
	}
	options, err := normalizeOptions(Options{ParameterPolicy: ParameterPolicyReport})
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
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parameters = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParameterCoveragePolicies(t *testing.T) {
	request := func(query, body string) Request {
		return Request{
			Method: "POST", URL: "https://example.test/api" + query,
			Header:       http.Header{"Content-Type": {"application/json"}},
			BodyCaptured: true, Body: []byte(body),
		}
	}
	t.Run("report only tracks newly discovered names", func(t *testing.T) {
		session, err := NewSession(Options{ParameterPolicy: ParameterPolicyReport})
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		first := request("?q=1", `{"item":{"id":1}}`)
		decision, token, err := session.Reserve(first)
		if err != nil || decision.Action != ActionAllow {
			t.Fatalf("Reserve(first) = (%#v, %v)", decision, err)
		}
		if err := session.Commit(token); err != nil {
			t.Fatal(err)
		}
		second := request("?q=2", `{"item":{"id":2}}`)
		decision, token, err = session.Reserve(second)
		if err != nil || decision.Action != ActionAllow {
			t.Fatalf("Reserve(second) = (%#v, %v)", decision, err)
		}
		if len(decision.NewParameters) != 0 {
			t.Fatalf("new parameters = %#v, want none", decision.NewParameters)
		}
		if err := session.Release(token); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("coverage mode drops covered shapes and allows new names", func(t *testing.T) {
		session, err := NewSession(Options{ParameterPolicy: ParameterPolicyCoverage})
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		first := request("?q=1", `{"item":{"id":1}}`)
		decision, token, err := session.Reserve(first)
		if err != nil || decision.Action != ActionAllow {
			t.Fatalf("Reserve(first) = (%#v, %v)", decision, err)
		}
		if err := session.Commit(token); err != nil {
			t.Fatal(err)
		}
		covered := request("?q=2", `{"item":{"id":2}}`)
		decision, _, err = session.Reserve(covered)
		if err != nil || decision.Action != ActionDrop || decision.Reason != ReasonParameterCoverage {
			t.Fatalf("Reserve(covered) = (%#v, %v)", decision, err)
		}
		newParameter := request("?q=2", `{"item":{"id":2},"email":"x"}`)
		decision, token, err = session.Reserve(newParameter)
		if err != nil || decision.Action != ActionAllow {
			t.Fatalf("Reserve(newParameter) = (%#v, %v)", decision, err)
		}
		want := []Parameter{{Location: ParameterJSON, Name: "/email"}}
		if !reflect.DeepEqual(decision.NewParameters, want) {
			t.Fatalf("new parameters = %#v, want %#v", decision.NewParameters, want)
		}
		if err := session.Commit(token); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCoverageKeepsParameterLocationsDistinct(t *testing.T) {
	session, err := NewSession(Options{ParameterPolicy: ParameterPolicyCoverage})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	first := Request{
		Method: "POST", URL: "https://example.test/api",
		Header:       http.Header{"Content-Type": {"application/json"}},
		BodyCaptured: true, Body: []byte(`{"id":1}`),
	}
	decision, token, err := session.Reserve(first)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(first) = (%#v, %v)", decision, err)
	}
	if err := session.Commit(token); err != nil {
		t.Fatal(err)
	}
	second := first
	second.URL += "?id=1"
	decision, token, err = session.Reserve(second)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(second) = (%#v, %v), want a new query parameter", decision, err)
	}
	want := []Parameter{{Location: ParameterQuery, Name: "id"}}
	if !reflect.DeepEqual(decision.NewParameters, want) {
		t.Fatalf("new parameters = %#v, want %#v", decision.NewParameters, want)
	}
	if err := session.Commit(token); err != nil {
		t.Fatal(err)
	}
}
