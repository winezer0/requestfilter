package nethttpadapterexample

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToFilterRequestMapsRelativeAndAbsoluteURLs(t *testing.T) {
	tests := []struct {
		name     string
		request  *http.Request
		wantURL  string
		wantBody string
	}{
		{
			name:    "relative HTTP URL",
			request: httptest.NewRequest(http.MethodPost, "/path?q=1", strings.NewReader("id=one")),
			wantURL: "http://example.com/path?q=1", wantBody: "id=one",
		},
		{
			name: "relative HTTPS URL",
			request: func() *http.Request {
				request := httptest.NewRequest(http.MethodGet, "/secure", nil)
				request.Host = "secure.example"
				request.TLS = &tls.ConnectionState{}
				return request
			}(),
			wantURL: "https://secure.example/secure",
		},
		{
			name:    "absolute proxy URL",
			request: httptest.NewRequest(http.MethodGet, "https://origin.example/item", nil),
			wantURL: "https://origin.example/item",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			filterRequest, err := toFilterRequest(test.request, "profile-1")
			if err != nil {
				t.Fatal(err)
			}
			if filterRequest.URL != test.wantURL || string(filterRequest.Body) != test.wantBody || !filterRequest.BodyCaptured {
				t.Fatalf("toFilterRequest() = (%q, %q, %t), want (%q, %q, true)",
					filterRequest.URL, filterRequest.Body, filterRequest.BodyCaptured, test.wantURL, test.wantBody)
			}
			if filterRequest.AuthenticationGroupID != "profile-1" {
				t.Fatalf("AuthenticationGroupID = %q, want profile-1", filterRequest.AuthenticationGroupID)
			}
			if filterRequest.Header == nil {
				t.Fatal("Header is nil; expected a cloned header map")
			}
		})
	}
}

func TestToFilterRequestRestoresBodyAfterSuccessAndOverflow(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("exact"))
		mapped, err := toFilterRequestWithLimit(request, "", 5)
		if err != nil || string(mapped.Body) != "exact" {
			t.Fatalf("toFilterRequestWithLimit() = (%#v, %v), want captured body", mapped, err)
		}
		assertRemainingBody(t, request, "exact")
	})

	t.Run("one byte over limit", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("toolong"))
		if _, err := toFilterRequestWithLimit(request, "", 6); err != errBodyTooLarge {
			t.Fatalf("toFilterRequestWithLimit() error = %v, want errBodyTooLarge", err)
		}
		assertRemainingBody(t, request, "toolong")
	})
}

func TestToFilterRequestRestoresBodyOnReadError(t *testing.T) {
	readFailure := errors.New("reader failed")
	request := httptest.NewRequest(http.MethodPost, "/", &failingBody{failure: readFailure})
	if _, err := toFilterRequestWithLimit(request, "", 32); err != readFailure {
		t.Fatalf("toFilterRequestWithLimit() error = %v, want read failure", err)
	}
	partial, err := io.ReadAll(request.Body)
	if err != readFailure || string(partial) != "partial" {
		t.Fatalf("restored body = (%q, %v), want partial bytes and original error", partial, err)
	}
}

func TestToFilterRequestRejectsMissingHostWithoutConsumingBody(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/path", strings.NewReader("body"))
	request.Host = ""
	if _, err := toFilterRequest(request, ""); err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("toFilterRequest() error = %v, want missing host", err)
	}
	assertRemainingBody(t, request, "body")
}

func TestToFilterRequestHandlesNilBodyAndClonesHeaders(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Body = nil
	request.Header.Set("X-Example", "before")
	mapped, err := toFilterRequest(request, "")
	if err != nil || mapped.Body == nil || !mapped.BodyCaptured {
		t.Fatalf("toFilterRequest() = (%#v, %v), want captured empty body", mapped, err)
	}
	request.Header.Set("X-Example", "after")
	if got := mapped.Header.Get("X-Example"); got != "before" {
		t.Fatalf("mapped header changed with source map: %q", got)
	}
}

func assertRemainingBody(t *testing.T, request *http.Request, want string) {
	t.Helper()
	got, err := io.ReadAll(request.Body)
	if err != nil || !bytes.Equal(got, []byte(want)) {
		t.Fatalf("remaining request body = (%q, %v), want %q", got, err, want)
	}
}

type failingBody struct {
	read    bool
	failure error
}

func (body *failingBody) Read(buffer []byte) (int, error) {
	if body.read {
		return 0, body.failure
	}
	body.read = true
	return copy(buffer, "partial"), body.failure
}

func (body *failingBody) Close() error { return nil }
