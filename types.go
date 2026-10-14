// Package requestfilter provides batch and session based HTTP request deduplication.
package requestfilter

import (
	"errors"
	"net/http"
	"sort"
	"time"
)

var (
	// ErrInvalidOptions reports invalid filter configuration.
	ErrInvalidOptions = errors.New("requestfilter: invalid options")
	// ErrInvalidRequest reports malformed or oversized request input.
	ErrInvalidRequest = errors.New("requestfilter: invalid request")
	// ErrSessionClosed reports an operation on a closed session.
	ErrSessionClosed = errors.New("requestfilter: session is closed")
	// ErrReservationNotFound reports an unknown, expired, or finalized reservation.
	ErrReservationNotFound = errors.New("requestfilter: reservation not found")
)

// Request contains the HTTP request fields used for filtering.
type Request struct {
	Method        string
	URL           string
	Header        http.Header
	Body          []byte
	BodyCaptured  bool
	AuthContextID string
}

// ParameterPolicy controls how parsed parameters affect decisions.
type ParameterPolicy uint8

const (
	// ParameterPolicyNone disables parameter extraction.
	ParameterPolicyNone ParameterPolicy = iota
	// ParameterPolicyCoverage drops requests whose parameter set is already covered.
	ParameterPolicyCoverage
	// ParameterPolicyIgnoreEmpty drops requests with complete parameter parsing and no parameters.
	ParameterPolicyIgnoreEmpty
)

// Options configures request identity, parameter coverage, and session limits.
type Options struct {
	ParameterPolicy        ParameterPolicy
	FingerprintHeaderNames []string
	MaxEntries             int
	MaxURLBytes            int
	MaxBodyBytes           int
	MaxParameters          int
	MaxJSONDepth           int
	// MaxXMLDepth limits XML nesting; zero uses the package default.
	MaxXMLDepth int
	// MaxXMLNodes bounds XML elements and non-namespace attributes; zero uses the package default.
	MaxXMLNodes int
	// MaxMultipartParts bounds parsed multipart parts; zero uses the package default.
	MaxMultipartParts int
	EntryTTL          time.Duration
	ReservationTTL    time.Duration
}

// Action describes what the caller should do with a request.
type Action string

const (
	// ActionAllow means the request has a live reservation and may be forwarded.
	ActionAllow Action = "allow"
	// ActionDrop means the request is already covered or was previously committed.
	ActionDrop Action = "drop"
	// ActionRetry means another pending request may establish the same coverage.
	ActionRetry Action = "retry"
	// ActionReject means the session cannot safely record more filtering state.
	ActionReject Action = "reject"
)

// Reason identifies why a request received its action.
type Reason string

const (
	ReasonNewRequest        Reason = "new_request"
	ReasonExactDuplicate    Reason = "exact_duplicate"
	ReasonParameterCoverage Reason = "parameter_coverage"
	ReasonNoParameters      Reason = "no_parameters"
	ReasonInFlight          Reason = "in_flight"
	ReasonCapacity          Reason = "capacity"
)

// ParameterLocation identifies where a parameter was parsed from.
type ParameterLocation string

const (
	ParameterQuery ParameterLocation = "query"
	ParameterForm  ParameterLocation = "form"
	ParameterJSON  ParameterLocation = "json_body"
	// ParameterXML identifies XML element and attribute paths.
	ParameterXML ParameterLocation = "xml_body"
	// ParameterMultipartField identifies a regular multipart form field.
	ParameterMultipartField ParameterLocation = "multipart_field"
	// ParameterMultipartFile identifies a multipart file field by its field name.
	ParameterMultipartFile ParameterLocation = "multipart_file"
)

// Parameter identifies a parameter without retaining its value.
type Parameter struct {
	Location ParameterLocation
	Name     string
}

// ParameterStatus reports whether parameter extraction completed.
type ParameterStatus string

const (
	ParameterStatusNotRequested ParameterStatus = "not_requested"
	ParameterStatusComplete     ParameterStatus = "complete"
	ParameterStatusPartial      ParameterStatus = "partial"
)

// Decision is the filter result for one request.
type Decision struct {
	Action          Action
	Reason          Reason
	Fingerprint     string
	ParameterStatus ParameterStatus
}

// Reservation is an opaque token used to commit or release an allowed request.
type Reservation struct {
	sessionID [16]byte
	id        uint64
}

// BatchResult contains accepted requests and one decision per input request.
type BatchResult struct {
	Accepted  []Request
	Decisions []Decision
}

type normalizedOptions struct {
	Options
	fingerprintHeaderNames []string
}

func normalizeOptions(options Options) (normalizedOptions, error) {
	if options.ParameterPolicy > ParameterPolicyIgnoreEmpty || options.EntryTTL < 0 || options.ReservationTTL < 0 {
		return normalizedOptions{}, ErrInvalidOptions
	}
	if options.MaxEntries == 0 {
		options.MaxEntries = 10000
	}
	if options.MaxURLBytes == 0 {
		options.MaxURLBytes = 16 << 10
	}
	if options.MaxBodyBytes == 0 {
		options.MaxBodyBytes = 8 << 20
	}
	if options.MaxParameters == 0 {
		options.MaxParameters = 4096
	}
	if options.MaxJSONDepth == 0 {
		options.MaxJSONDepth = 64
	}
	if options.MaxXMLDepth == 0 {
		options.MaxXMLDepth = 64
	}
	if options.MaxXMLNodes == 0 {
		options.MaxXMLNodes = 10000
	}
	if options.MaxMultipartParts == 0 {
		options.MaxMultipartParts = 4096
	}
	if options.ReservationTTL == 0 {
		options.ReservationTTL = 30 * time.Second
	}
	if options.MaxEntries < 1 || options.MaxURLBytes < 1 || options.MaxBodyBytes < 0 || options.MaxParameters < 1 ||
		options.MaxJSONDepth < 1 || options.MaxXMLDepth < 1 || options.MaxXMLNodes < 1 || options.MaxMultipartParts < 1 {
		return normalizedOptions{}, ErrInvalidOptions
	}
	seen := make(map[string]struct{}, len(options.FingerprintHeaderNames))
	for _, name := range options.FingerprintHeaderNames {
		canonical := http.CanonicalHeaderKey(name)
		if canonical == "" || !validToken(canonical) {
			return normalizedOptions{}, ErrInvalidOptions
		}
		seen[canonical] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	options.FingerprintHeaderNames = append([]string(nil), names...)
	return normalizedOptions{Options: options, fingerprintHeaderNames: names}, nil
}

func validToken(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || containsTokenPunctuation(character) {
			continue
		}
		return false
	}
	return true
}

func containsTokenPunctuation(character rune) bool {
	return character == '!' || character == '#' || character == '$' || character == '%' || character == '&' ||
		character == '\'' || character == '*' || character == '+' || character == '-' || character == '.' ||
		character == '^' || character == '_' || character == '`' || character == '|' || character == '~'
}
