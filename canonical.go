package requestfilter

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type normalizedRequest struct {
	request         Request
	method          string
	canonicalURL    string
	endpointPath    string
	mediaType       string
	contentType     string
	authContextID   string
	fingerprint     string
	endpointKey     string
	parameters      []Parameter
	parameterStatus ParameterStatus
}

func normalizeRequest(request Request, options normalizedOptions) (normalizedRequest, error) {
	if len(request.URL) > options.MaxURLBytes || len(request.Body) > options.MaxBodyBytes || !request.BodyCaptured && len(request.Body) > 0 {
		return normalizedRequest{}, ErrInvalidRequest
	}
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = http.MethodGet
	}
	if !validToken(method) {
		return normalizedRequest{}, ErrInvalidRequest
	}
	canonicalURL, endpointPath, err := normalizeURL(request.URL)
	if err != nil || len(canonicalURL) > options.MaxURLBytes {
		return normalizedRequest{}, ErrInvalidRequest
	}
	contentType, mediaType := normalizedContentType(headerValues(request.Header, "Content-Type"))
	authContextID := strings.TrimSpace(request.AuthContextID)
	if len(authContextID) > 1024 || strings.ContainsAny(authContextID, "\r\n\x00") {
		return normalizedRequest{}, ErrInvalidRequest
	}
	endpointKey := digestFields(method, endpointPath, mediaType, authContextID)
	parts := [][]byte{
		[]byte(method), []byte(canonicalURL), []byte(contentType),
		[]byte(strconv.FormatBool(request.BodyCaptured)), request.Body,
		[]byte(authContextID),
	}
	for _, headerName := range options.fingerprintHeaderNames {
		parts = append(parts, []byte(headerName))
		for _, value := range headerValues(request.Header, headerName) {
			parts = append(parts, []byte(strings.TrimSpace(value)))
		}
		parts = append(parts, nil)
	}
	fingerprint := digestParts(parts...)
	return normalizedRequest{
		request: request, method: method, canonicalURL: canonicalURL,
		endpointPath: endpointPath, mediaType: mediaType, contentType: contentType,
		authContextID: authContextID, fingerprint: fingerprint, endpointKey: endpointKey,
		parameterStatus: ParameterStatusNotRequested,
	}, nil
}

func normalizeURL(raw string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Hostname() == "" {
		return "", "", ErrInvalidRequest
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "", ErrInvalidRequest
	}
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if hostname == "" {
		return "", "", ErrInvalidRequest
	}
	port := parsed.Port()
	if port != "" {
		portNumber, parseErr := strconv.Atoi(port)
		if parseErr != nil || portNumber < 1 || portNumber > 65535 {
			return "", "", ErrInvalidRequest
		}
		port = strconv.Itoa(portNumber)
	}
	if scheme == "http" && port == "80" || scheme == "https" && port == "443" {
		port = ""
	}
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		host += ":" + port
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "", "", ErrInvalidRequest
	}
	endpointPath := scheme + "://" + host + path
	canonicalURL := endpointPath
	if parsed.RawQuery != "" {
		canonicalURL += "?" + parsed.RawQuery
	}
	return canonicalURL, endpointPath, nil
}

func normalizedContentType(values []string) (string, string) {
	if len(values) == 0 {
		return "", ""
	}
	normalized := make([]string, 0, len(values))
	mediaTypes := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		mediaType, parameters, err := mime.ParseMediaType(trimmed)
		if err != nil {
			lower := strings.ToLower(trimmed)
			normalized = append(normalized, lower)
			mediaTypes = append(mediaTypes, lower)
			continue
		}
		mediaType = strings.ToLower(mediaType)
		canonical := mime.FormatMediaType(mediaType, parameters)
		normalized = append(normalized, canonical)
		mediaTypes = append(mediaTypes, mediaType)
	}
	return strings.Join(normalized, "\x00"), strings.Join(mediaTypes, "\x00")
}

func headerValues(header http.Header, name string) []string {
	type matchedHeader struct {
		name   string
		values []string
	}
	var matched []matchedHeader
	for key, values := range header {
		if strings.EqualFold(key, name) {
			matched = append(matched, matchedHeader{name: key, values: values})
		}
	}
	sort.Slice(matched, func(left, right int) bool {
		return matched[left].name < matched[right].name
	})
	var values []string
	for _, entry := range matched {
		values = append(values, entry.values...)
	}
	return values
}

func digestFields(fields ...string) string {
	parts := make([][]byte, len(fields))
	for index, field := range fields {
		parts[index] = []byte(field)
	}
	return digestParts(parts...)
}

func digestParts(parts ...[]byte) string {
	digest := sha256.New()
	for _, part := range parts {
		writePart(digest, part)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func writePart(digest hash.Hash, part []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(part)))
	_, _ = digest.Write(length[:])
	_, _ = digest.Write(part)
}
