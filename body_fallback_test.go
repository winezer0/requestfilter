package requestfilter

import (
	"encoding/binary"
	"net/http"
	"testing"
	"unicode/utf16"
)

func TestDedupStrategyCoverageParametersParseFailureAllowsWithoutStrategyState(t *testing.T) {
	session, err := NewSession(Options{DedupStrategy: DedupStrategyCoverageParameters})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	request := Request{
		Method: "POST", URL: "https://example.test/api",
		Header:       http.Header{"Content-Type": {"application/json"}},
		BodyCaptured: true, Body: []byte(`{"id":`),
	}
	decision, token, err := session.Reserve(request)
	if err != nil || decision.Action != ActionAllow || decision.ParameterStatus != ParameterStatusPartial {
		t.Fatalf("Reserve(malformed) = (%#v, %v)", decision, err)
	}
	if err := session.Commit(token); err != nil {
		t.Fatal(err)
	}
	decision, token, err = session.Reserve(request)
	if err != nil || decision.Action != ActionAllow || decision.ParameterStatus != ParameterStatusPartial {
		t.Fatalf("Reserve(repeated malformed) = (%#v, %v), want partial allow", decision, err)
	}
	if err := session.Commit(token); err != nil {
		t.Fatal(err)
	}
}

func TestIncompleteBodiesBypassDedupStrategyCoverageParameters(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		firstBody   []byte
		secondBody  []byte
	}{
		{name: "malformed JSON", contentType: "application/json", firstBody: []byte(`{"id":`), secondBody: []byte(`{"name":`)},
		{name: "malformed XML", contentType: "application/xml", firstBody: []byte(`<root><id>1</root>`), secondBody: []byte(`<root><name>1</root>`)},
		{
			name: "unsupported UTF-16 XML", contentType: "application/xml",
			firstBody: utf16XML(`<root><id>1</id></root>`), secondBody: utf16XML(`<root><id>2</id></root>`),
		},
		{name: "malformed form", contentType: "application/x-www-form-urlencoded", firstBody: []byte("id=%zz"), secondBody: []byte("name=%zz")},
		{
			name: "truncated multipart upload", contentType: "multipart/form-data; boundary=broken",
			firstBody:  []byte("--broken\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.txt\"\r\n\r\na"),
			secondBody: []byte("--broken\r\nContent-Disposition: form-data; name=\"file\"; filename=\"b.txt\"\r\n\r\nb"),
		},
		{name: "unknown binary format", contentType: "application/octet-stream", firstBody: []byte{0, 1, 2}, secondBody: []byte{0, 1, 3}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session, err := NewSession(Options{DedupStrategy: DedupStrategyCoverageParameters})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			makeRequest := func(body []byte) Request {
				return Request{
					Method: "POST", URL: "https://example.test/upload",
					Header: http.Header{"Content-Type": {test.contentType}}, BodyCaptured: true, Body: body,
				}
			}
			first, token, err := session.Reserve(makeRequest(test.firstBody))
			if err != nil || first.Action != ActionAllow || first.ParameterStatus != ParameterStatusPartial {
				t.Fatalf("Reserve(first) = (%#v, %v), want partial allow", first, err)
			}
			if err := session.Commit(token); err != nil {
				t.Fatal(err)
			}
			second, token, err := session.Reserve(makeRequest(test.secondBody))
			if err != nil || second.Action != ActionAllow || second.ParameterStatus != ParameterStatusPartial {
				t.Fatalf("Reserve(second) = (%#v, %v), want partial allow", second, err)
			}
			if err := session.Commit(token); err != nil {
				t.Fatal(err)
			}
			repeated, token, err := session.Reserve(makeRequest(test.secondBody))
			if err != nil || repeated.Action != ActionAllow || repeated.ParameterStatus != ParameterStatusPartial {
				t.Fatalf("Reserve(repeated) = (%#v, %v), want partial allow", repeated, err)
			}
			if err := session.Commit(token); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func utf16XML(document string) []byte {
	encoded := utf16.Encode([]rune(`<?xml version="1.0" encoding="UTF-16"?>` + document))
	result := make([]byte, 2+2*len(encoded))
	result[0], result[1] = 0xff, 0xfe
	for index, value := range encoded {
		binary.LittleEndian.PutUint16(result[2+index*2:], value)
	}
	return result
}

func TestParameterLimitBypassesDedupStrategyCoverageParameters(t *testing.T) {
	session, err := NewSession(Options{DedupStrategy: DedupStrategyCoverageParameters, MaxParameters: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	first := Request{URL: "https://example.test/?a=1&b=2", BodyCaptured: true}
	decision, token, err := session.Reserve(first)
	if err != nil || decision.Action != ActionAllow || decision.ParameterStatus != ParameterStatusPartial {
		t.Fatalf("Reserve(first) = (%#v, %v), want partial allow", decision, err)
	}
	if err := session.Commit(token); err != nil {
		t.Fatal(err)
	}
	second := Request{URL: "https://example.test/?a=1&c=2", BodyCaptured: true}
	decision, token, err = session.Reserve(second)
	if err != nil || decision.Action != ActionAllow || decision.ParameterStatus != ParameterStatusPartial {
		t.Fatalf("Reserve(second) = (%#v, %v), want partial allow", decision, err)
	}
	if err := session.Commit(token); err != nil {
		t.Fatal(err)
	}
	decision, reservation, err := session.Reserve(second)
	if err != nil || decision.Action != ActionAllow || decision.ParameterStatus != ParameterStatusPartial {
		t.Fatalf("Reserve(repeated) = (%#v, %v), want partial allow", decision, err)
	}
	if err := session.Commit(reservation); err != nil {
		t.Fatal(err)
	}
}
