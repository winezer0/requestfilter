package requestfilter

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"
)

type referenceCorpus struct {
	Metadata struct {
		Repository string          `json:"reference_repository"`
		Commit     string          `json:"reference_commit"`
		Oracle     string          `json:"oracle"`
		Evidence   []string        `json:"evidence"`
		Rules      []referenceRule `json:"rules"`
	} `json:"metadata"`
	Cases []referenceCorpusCase `json:"cases"`
}

type referenceRule struct {
		Area            string `json:"area"`
	SourceBehavior  string `json:"source_behavior"`
	PackageBehavior string `json:"package_behavior"`
	Difference      string `json:"difference"`
	Source          string `json:"source"`
}

type referenceCorpusCase struct {
	Name             string      `json:"name"`
	First            corpusInput `json:"first"`
	Second           corpusInput `json:"second"`
	Status           string      `json:"status"`
	Parameters       []Parameter `json:"parameters"`
	SecondParameters []Parameter `json:"second_parameters"`
	SecondAction     Action      `json:"second_action"`
	ReferenceOutcome string      `json:"reference_outcome"`
	ReferencePolicy  string      `json:"reference_policy"`
}

type corpusInput struct {
	Method       string      `json:"method"`
	URL          string      `json:"url"`
	Headers      http.Header `json:"headers"`
	Body         string      `json:"body"`
	BodyCaptured bool        `json:"body_captured"`
}

func (input corpusInput) request() Request {
	return Request{
		Method: input.Method, URL: input.URL, Header: input.Headers,
		Body: []byte(input.Body), BodyCaptured: input.BodyCaptured,
	}
}

func TestReferenceBehaviorCorpus(t *testing.T) {
	data, err := os.ReadFile("testdata/reference-corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus referenceCorpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	if corpus.Metadata.Repository == "" || corpus.Metadata.Commit == "" || corpus.Metadata.Oracle == "" || len(corpus.Metadata.Evidence) == 0 {
		t.Fatal("reference corpus must identify its source snapshot and oracle limits")
	}
	wantAreas := map[string]bool{
		"DedupStrategySelectedFieldsHash": false,
		"DedupStrategyRequireParameters": false,
		"DedupStrategyCoverageParameters": false,
		"AuthenticationGrouping":         false,
	}
	for _, rule := range corpus.Metadata.Rules {
		if _, ok := wantAreas[rule.Area]; !ok {
			t.Errorf("unknown reference rule area %q", rule.Area)
			continue
		}
		if wantAreas[rule.Area] || rule.SourceBehavior == "" || rule.PackageBehavior == "" || rule.Difference == "" || rule.Source == "" {
			t.Errorf("reference rule %q is duplicate or incomplete: %#v", rule.Area, rule)
			continue
		}
		wantAreas[rule.Area] = true
	}
	for area, found := range wantAreas {
		if !found {
			t.Errorf("reference corpus is missing the %s source rule", area)
		}
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("reference corpus must contain cases")
	}

	options, err := normalizeOptions(Options{DedupStrategy: DedupStrategyCoverageParameters})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range corpus.Cases {
		t.Run(test.Name, func(t *testing.T) {
			if test.ReferencePolicy == "" || test.ReferenceOutcome == "" {
				t.Fatal("case is missing its source-derived reference outcome")
			}
			if test.ReferenceOutcome != "conditional_duplicate" && test.ReferenceOutcome != "not_determined_by_source" {
				t.Fatalf("unknown source-derived reference outcome %q", test.ReferenceOutcome)
			}
			first := assertCorpusParameters(t, test.First.request(), options, test.Status, test.Parameters)
			secondParameters := test.Parameters
			if len(test.SecondParameters) > 0 {
				secondParameters = test.SecondParameters
			}
			assertCorpusParameters(t, test.Second.request(), options, test.Status, secondParameters)

			session, err := NewSession(Options{DedupStrategy: DedupStrategyCoverageParameters})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()

			decision, reservation, err := session.Reserve(test.First.request())
			if err != nil || decision.Action != ActionAllow {
				t.Fatalf("Reserve(first) = (%#v, %v), want allow", decision, err)
			}
			if first.parameterStatus == ParameterStatusComplete {
				if err := session.Commit(reservation); err != nil {
					t.Fatal(err)
				}
			}
			repeated, _, err := session.Reserve(test.Second.request())
			if err != nil || repeated.Action != test.SecondAction {
				t.Fatalf("Reserve(second) = (%#v, %v), want action %q", repeated, err, test.SecondAction)
			}
		})
	}
}

type corpusParseResult struct {
	parameterStatus ParameterStatus
}

func assertCorpusParameters(t *testing.T, request Request, options normalizedOptions, wantStatus string, want []Parameter) corpusParseResult {
	t.Helper()
	normalized, err := normalizeRequest(request, options)
	if err != nil {
		t.Fatal(err)
	}
	got, status, err := extractParameters(normalized, options)
	if err != nil {
		t.Fatal(err)
	}
	if string(status) != wantStatus {
		t.Fatalf("extractParameters() status = %q, want %q", status, wantStatus)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractParameters() = %#v, want %#v", got, want)
	}
	return corpusParseResult{parameterStatus: status}
}
