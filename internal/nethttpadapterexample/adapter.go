// Package nethttpadapterexample contains the tested net/http mapping shown in
// the repository README. It is an internal example, not part of the public API.
package nethttpadapterexample

import (
	"bytes"
	"errors"
	"io"
	"net/http"

	"github.com/winezer0/requestfilter"
)

const maxCapturedBodyBytes int64 = 8 << 20

var errBodyTooLarge = errors.New("request body exceeds filter limit")

func toFilterRequest(r *http.Request, authenticationGroupID string) (requestfilter.Request, error) {
	return toFilterRequestWithLimit(r, authenticationGroupID, maxCapturedBodyBytes)
}

func toFilterRequestWithLimit(r *http.Request, authenticationGroupID string, maximum int64) (requestfilter.Request, error) {
	if r == nil || r.URL == nil || maximum < 0 || maximum == int64(^uint64(0)>>1) {
		return requestfilter.Request{}, errors.New("invalid HTTP request or body limit")
	}
	url := r.URL.String()
	if !r.URL.IsAbs() {
		if r.Host == "" {
			return requestfilter.Request{}, errors.New("missing request host")
		}
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		absoluteURL := *r.URL
		absoluteURL.Scheme = scheme
		absoluteURL.Host = r.Host
		url = absoluteURL.String()
	}

	bodyReader := r.Body
	if bodyReader == nil {
		bodyReader = http.NoBody
	}
	body, err := io.ReadAll(io.LimitReader(bodyReader, maximum+1))
	restoreBody := func() {
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), bodyReader))
	}
	if err != nil {
		restoreBody()
		return requestfilter.Request{}, err
	}
	if int64(len(body)) > maximum {
		restoreBody()
		return requestfilter.Request{}, errBodyTooLarge
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	return requestfilter.Request{
		Method: r.Method, URL: url, Header: r.Header.Clone(),
		Body: body, BodyCaptured: true, AuthenticationGroupID: authenticationGroupID,
	}, nil
}
