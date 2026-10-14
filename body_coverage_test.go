package requestfilter

import (
	"net/http"
	"testing"
)

func TestStructuredBodyFormatsParticipateInCoverage(t *testing.T) {
	tests := []struct {
		name         string
		contentType  string
		firstBody    string
		coveredBody  string
		newShapeBody string
	}{
		{
			name:        "XML",
			contentType: "application/xml",
			firstBody:   `<root><id>1</id></root>`, coveredBody: `<root><id>2</id></root>`,
			newShapeBody: `<root><id>2</id><email>x</email></root>`,
		},
		{
			name:        "url encoded form",
			contentType: "application/x-www-form-urlencoded",
			firstBody:   "id=1", coveredBody: "id=2", newShapeBody: "id=2&email=x",
		},
		{
			name:         "multipart fields and file fields",
			contentType:  "multipart/form-data; boundary=coverage",
			firstBody:    multipartCoverageBody("1", "a.txt", "a"),
			coveredBody:  multipartCoverageBody("2", "b.txt", "b"),
			newShapeBody: multipartCoverageBodyWithEmail("2", "b.txt", "b"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session, err := NewSession(Options{ParameterPolicy: ParameterPolicyCoverage})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			makeRequest := func(body string) Request {
				return Request{
					Method: "POST", URL: "https://example.test/data",
					Header:       http.Header{"Content-Type": {test.contentType}},
					BodyCaptured: true, Body: []byte(body),
				}
			}
			first, token, err := session.Reserve(makeRequest(test.firstBody))
			if err != nil || first.Action != ActionAllow || first.ParameterStatus != ParameterStatusComplete {
				t.Fatalf("Reserve(first) = (%#v, %v), want complete allow", first, err)
			}
			if err := session.Commit(token); err != nil {
				t.Fatal(err)
			}
			covered, _, err := session.Reserve(makeRequest(test.coveredBody))
			if err != nil || covered.Action != ActionDrop || covered.Reason != ReasonParameterCoverage {
				t.Fatalf("Reserve(covered) = (%#v, %v), want parameter coverage drop", covered, err)
			}
			newShape, token, err := session.Reserve(makeRequest(test.newShapeBody))
			if err != nil || newShape.Action != ActionAllow || newShape.ParameterStatus != ParameterStatusComplete {
				t.Fatalf("Reserve(new shape) = (%#v, %v), want complete allow", newShape, err)
			}
			if err := session.Commit(token); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUncapturedBodiesNeverUseParameterCoverage(t *testing.T) {
	for _, method := range []string{"GET", "HEAD"} {
		t.Run(method, func(t *testing.T) {
			session, err := NewSession(Options{ParameterPolicy: ParameterPolicyCoverage})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			makeRequest := func(query string) Request {
				return Request{
					Method: method, URL: "https://example.test/data?" + query,
					Header: http.Header{"Content-Type": {"application/x-unknown"}},
				}
			}
			first, token, err := session.Reserve(makeRequest("first=1"))
			if err != nil || first.Action != ActionAllow || first.ParameterStatus != ParameterStatusPartial {
				t.Fatalf("Reserve(first) = (%#v, %v), want partial allow", first, err)
			}
			if err := session.Commit(token); err != nil {
				t.Fatal(err)
			}
			second, _, err := session.Reserve(makeRequest("second=1"))
			if err != nil || second.Action != ActionAllow || second.ParameterStatus != ParameterStatusPartial {
				t.Fatalf("Reserve(second) = (%#v, %v), want partial allow", second, err)
			}
		})
	}
}

func multipartCoverageBody(caption, filename, content string) string {
	return "--coverage\r\nContent-Disposition: form-data; name=\"caption\"\r\n\r\n" + caption +
		"\r\n--coverage\r\nContent-Disposition: form-data; name=\"upload\"; filename=\"" + filename +
		"\"\r\nContent-Type: text/plain\r\n\r\n" + content + "\r\n--coverage--\r\n"
}

func multipartCoverageBodyWithEmail(caption, filename, content string) string {
	return "--coverage\r\nContent-Disposition: form-data; name=\"caption\"\r\n\r\n" + caption +
		"\r\n--coverage\r\nContent-Disposition: form-data; name=\"upload\"; filename=\"" + filename +
		"\"\r\nContent-Type: text/plain\r\n\r\n" + content +
		"\r\n--coverage\r\nContent-Disposition: form-data; name=\"email\"\r\n\r\nx\r\n--coverage--\r\n"
}
