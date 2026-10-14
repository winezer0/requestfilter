package nethttpadapterexample

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/winezer0/requestfilter"
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

func TestToFilterRequestPreservesChunkedHTTPBody(t *testing.T) {
	const wantBody = "chunk-one-chunk-two"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if len(request.TransferEncoding) == 0 || request.TransferEncoding[0] != "chunked" {
			t.Errorf("TransferEncoding = %v, want chunked", request.TransferEncoding)
		}
		mapped, err := toFilterRequest(request, "")
		if err != nil {
			t.Errorf("toFilterRequest() error = %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if string(mapped.Body) != wantBody {
			t.Errorf("captured body = %q, want %q", mapped.Body, wantBody)
		}
		remaining, err := io.ReadAll(request.Body)
		if err != nil || string(remaining) != wantBody {
			t.Errorf("forwarded body = (%q, %v), want %q", remaining, err, wantBody)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	reader, writer := io.Pipe()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/upload", reader)
	if err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := io.WriteString(writer, "chunk-one-")
		if writeErr == nil {
			_, writeErr = io.WriteString(writer, "chunk-two")
		}
		closeErr := writer.Close()
		if writeErr != nil {
			writeDone <- writeErr
			return
		}
		writeDone <- closeErr
	}()
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if err := <-writeDone; err != nil {
		t.Fatalf("write chunked request body: %v", err)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("response status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
}

func TestHTTPFlowDeduplicatesAndForwardsByAuthentication(t *testing.T) {
	const wantBody = `{"item":"value"}`
	filter, err := requestfilter.NewSession(requestfilter.Options{
		DedupStrategy: requestfilter.DedupStrategySelectedFieldsHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer filter.Close()

	var forwardedMu sync.Mutex
	forwardedBodies := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		filtered, err := toFilterRequest(request, "")
		if err != nil {
			t.Errorf("toFilterRequest() error = %v", err)
			http.Error(writer, "mapping failed", http.StatusBadRequest)
			return
		}
		decision, reservation, err := filter.Reserve(filtered)
		if err != nil {
			t.Errorf("Reserve() error = %v", err)
			http.Error(writer, "filter failed", http.StatusBadRequest)
			return
		}
		switch decision.Action {
		case requestfilter.ActionAllow:
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read forwarded request body: %v", err)
				_ = filter.Release(reservation)
				http.Error(writer, "body read failed", http.StatusBadRequest)
				return
			}
			forwardedMu.Lock()
			forwardedBodies = append(forwardedBodies, string(body))
			forwardedMu.Unlock()
			if err := filter.Commit(reservation); err != nil {
				t.Errorf("Commit() error = %v", err)
				http.Error(writer, "commit failed", http.StatusServiceUnavailable)
				return
			}
			writer.WriteHeader(http.StatusAccepted)
		case requestfilter.ActionDrop:
			writer.WriteHeader(http.StatusNoContent)
		case requestfilter.ActionRetry, requestfilter.ActionReject:
			t.Errorf("unexpected decision action %q", decision.Action)
			http.Error(writer, "unexpected filter action", http.StatusServiceUnavailable)
		default:
			t.Errorf("unknown decision action %q", decision.Action)
			http.Error(writer, "unknown filter action", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := server.Client()
	for _, test := range []struct {
		name       string
		cookie     string
		wantStatus int
	}{
		{name: "first identity request", cookie: "session=profile-a", wantStatus: http.StatusAccepted},
		{name: "same identity duplicate", cookie: "session=profile-a", wantStatus: http.StatusNoContent},
		{name: "separate identity request", cookie: "session=profile-b", wantStatus: http.StatusAccepted},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, server.URL+"/api?item=1", strings.NewReader(wantBody))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Cookie", test.cookie)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.wantStatus)
			}
		})
	}

	if len(forwardedBodies) != 2 {
		t.Fatalf("forwarded body count = %d, want 2", len(forwardedBodies))
	}
	for index, body := range forwardedBodies {
		if body != wantBody {
			t.Errorf("forwarded body %d = %q, want %q", index, body, wantBody)
		}
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
