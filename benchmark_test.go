package requestfilter

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func BenchmarkExtractParametersJSON(b *testing.B) {
	var body strings.Builder
	body.WriteString(`{"payload":{`)
	for index := 0; index < 100; index++ {
		if index > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `"field_%03d":{"id":%d}`, index, index)
	}
	body.WriteString(`}}`)
	options, err := normalizeOptions(Options{DedupStrategy: DedupStrategyCoverageParameters})
	if err != nil {
		b.Fatal(err)
	}
	request, err := normalizeRequest(Request{
		Method: "POST", URL: "https://example.test/api",
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(body.String()), BodyCaptured: true,
	}, options)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(request.request.Body)))
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, _, err := extractParameters(request, options); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExtractParametersMultipart(b *testing.B) {
	var body strings.Builder
	writer := multipart.NewWriter(&body)
	for index := 0; index < 64; index++ {
		if err := writer.WriteField(fmt.Sprintf("field_%03d", index), "value"); err != nil {
			b.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		b.Fatal(err)
	}
	options, err := normalizeOptions(Options{DedupStrategy: DedupStrategyCoverageParameters})
	if err != nil {
		b.Fatal(err)
	}
	request, err := normalizeRequest(Request{
		Method: "POST", URL: "https://example.test/upload",
		Header: http.Header{"Content-Type": {writer.FormDataContentType()}},
		Body:   []byte(body.String()), BodyCaptured: true,
	}, options)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(request.request.Body)))
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, _, err := extractParameters(request, options); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDedupStrategyCoverageParametersCovered(b *testing.B) {
	session, err := NewSession(Options{DedupStrategy: DedupStrategyCoverageParameters})
	if err != nil {
		b.Fatal(err)
	}
	defer session.Close()
	request := Request{
		Method: "GET", URL: "https://example.test/items?a=1&b=2", BodyCaptured: true,
	}
	decision, token, err := session.Reserve(request)
	if err != nil || decision.Action != ActionAllow {
		b.Fatalf("Reserve(seed) = (%#v, %v), want allow", decision, err)
	}
	if err := session.Commit(token); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		decision, _, err := session.Reserve(request)
		if err != nil || decision.Action != ActionDrop {
			b.Fatalf("Reserve(covered) = (%#v, %v), want drop", decision, err)
		}
	}
}

func BenchmarkDedupStrategySelectedFieldsHashBatch(b *testing.B) {
	const requestCount = 64
	for _, test := range []struct {
		name   string
		unique bool
	}{
		{name: "duplicate", unique: false},
		{name: "unique", unique: true},
	} {
		b.Run(test.name, func(b *testing.B) {
			requests := make([]Request, requestCount)
			for index := range requests {
				url := "https://example.test/items?id=1"
				if test.unique {
					url = fmt.Sprintf("https://example.test/items?id=%d", index)
				}
				requests[index] = Request{Method: "GET", URL: url, BodyCaptured: true}
			}
			options := Options{DedupStrategy: DedupStrategySelectedFieldsHash}
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				if _, err := FilterBatch(requests, options); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
