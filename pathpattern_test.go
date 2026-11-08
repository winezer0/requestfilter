package requestfilter

import (
	"fmt"
	"testing"
)

// reserveAndCommit is a test helper that treats every allowed request as
// immediately accepted downstream, matching crawler consumption semantics.
func reserveAndCommit(t *testing.T, session *Session, url string) Action {
	t.Helper()
	decision, reservation, err := session.Reserve(Request{URL: url, BodyCaptured: true})
	if err != nil {
		t.Fatalf("Reserve(%s) error = %v", url, err)
	}
	if decision.Action == ActionAllow {
		if err := session.Commit(reservation); err != nil {
			t.Fatalf("Commit(%s) error = %v", url, err)
		}
	}
	return decision.Action
}

func TestPathSegmentThresholdFoldsAfterLearning(t *testing.T) {
	session, err := NewSession(Options{
		DedupStrategy:        DedupStrategyCoverageParameters,
		PathSegmentThreshold: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	// Learning phase: distinct values below the threshold are all allowed.
	for _, id := range []string{"1", "2"} {
		if action := reserveAndCommit(t, session, fmt.Sprintf("https://example.test/users/%s", id)); action != ActionAllow {
			t.Fatalf("users/%s during learning = %s, want allow", id, action)
		}
	}
	// The request that reaches the threshold folds the position and becomes the
	// representative (first request of the generalized coverage group).
	if action := reserveAndCommit(t, session, "https://example.test/users/3"); action != ActionAllow {
		t.Fatalf("users/3 at threshold = %s, want allow", action)
	}
	// After folding, further variants are covered and dropped.
	for _, id := range []string{"4", "456", "123456"} {
		if action := reserveAndCommit(t, session, fmt.Sprintf("https://example.test/users/%s", id)); action != ActionDrop {
			t.Fatalf("users/%s after fold = %s, want drop", id, action)
		}
	}
}

func TestPathSegmentThresholdSeparatesParentPaths(t *testing.T) {
	session, err := NewSession(Options{
		DedupStrategy:        DedupStrategyCoverageParameters,
		PathSegmentThreshold: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	// Fold /users/{dyn} first.
	for _, id := range []string{"1", "2", "3"} {
		reserveAndCommit(t, session, "https://example.test/users/"+id)
	}
	if action := reserveAndCommit(t, session, "https://example.test/users/999"); action != ActionDrop {
		t.Fatalf("users/999 = %s, want drop", action)
	}
	// /items/{dyn} is an independent learning group: first values stay allowed.
	if action := reserveAndCommit(t, session, "https://example.test/items/1"); action != ActionAllow {
		t.Fatalf("items/1 = %s, want allow", action)
	}
}

func TestPathSegmentThresholdMixedWithQueryCoverage(t *testing.T) {
	session, err := NewSession(Options{
		DedupStrategy:        DedupStrategyCoverageParameters,
		PathSegmentThreshold: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	// Fold /users/{dyn} while learning a query parameter at the same time.
	reserveAndCommit(t, session, "https://example.test/users/1?sort=name")
	reserveAndCommit(t, session, "https://example.test/users/2?sort=name")
	// After folding, the path segment is covered; the query parameter is also
	// already covered, so the whole shape is dropped.
	if action := reserveAndCommit(t, session, "https://example.test/users/3?sort=name"); action != ActionDrop {
		t.Fatalf("users/3?sort=name = %s, want drop", action)
	}
	// A new query parameter name keeps the request allowed (new shape).
	if action := reserveAndCommit(t, session, "https://example.test/users/4?order=desc"); action != ActionAllow {
		t.Fatalf("users/4?order=desc = %s, want allow", action)
	}
}

func TestPathSegmentThresholdZeroDisables(t *testing.T) {
	session, err := NewSession(Options{DedupStrategy: DedupStrategyCoverageParameters})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for id := 1; id <= 30; id++ {
		if action := reserveAndCommit(t, session, fmt.Sprintf("https://example.test/users/%d", id)); action != ActionAllow {
			t.Fatalf("users/%d with threshold disabled = %s, want allow", id, action)
		}
	}
}

func TestPathSegmentThresholdTrailingSlashEquivalent(t *testing.T) {
	session, err := NewSession(Options{
		DedupStrategy:        DedupStrategyCoverageParameters,
		PathSegmentThreshold: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	reserveAndCommit(t, session, "https://example.test/users/1/")
	reserveAndCommit(t, session, "https://example.test/users/2/")
	// A trailing slash must not defeat generalization.
	if action := reserveAndCommit(t, session, "https://example.test/users/3/"); action != ActionDrop {
		t.Fatalf("users/3/ = %s, want drop", action)
	}
	if action := reserveAndCommit(t, session, "https://example.test/users/3"); action != ActionDrop {
		t.Fatalf("users/3 (no slash) = %s, want drop", action)
	}
}

func TestPathSegmentThresholdNestedDynamicSegments(t *testing.T) {
	session, err := NewSession(Options{
		DedupStrategy:        DedupStrategyCoverageParameters,
		PathSegmentThreshold: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	// Learn both /users/{dyn} and the nested /users/{dyn}/orders/{dyn}.
	for _, pair := range [][2]string{{"1", "100"}, {"2", "200"}, {"3", "300"}} {
		reserveAndCommit(t, session, fmt.Sprintf("https://example.test/users/%s/orders/%s", pair[0], pair[1]))
	}
	if action := reserveAndCommit(t, session, "https://example.test/users/777/orders/888"); action != ActionDrop {
		t.Fatalf("users/777/orders/888 = %s, want drop", action)
	}
	// A different nested branch stays allowed because its parent differs.
	if action := reserveAndCommit(t, session, "https://example.test/users/1/profile"); action != ActionAllow {
		t.Fatalf("users/1/profile = %s, want allow", action)
	}
}

func TestPathSegmentThresholdAuthenticationIsolation(t *testing.T) {
	session, err := NewSession(Options{
		DedupStrategy:        DedupStrategyCoverageParameters,
		PathSegmentThreshold: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	authenticated := Request{
		URL:    "https://example.test/users/1",
		Header: map[string][]string{"Cookie": {"session=alice"}},
	}
	anonymous := Request{URL: "https://example.test/users/1", BodyCaptured: true}

	decision, reservation, err := session.Reserve(authenticated)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(authenticated) = (%s, %v)", decision.Action, err)
	}
	if err := session.Commit(reservation); err != nil {
		t.Fatal(err)
	}
	// Same URL, different authentication context: independently allowed.
	decision, _, err = session.Reserve(anonymous)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(anonymous) = (%s, %v)", decision.Action, err)
	}
}

func TestPathSegmentThresholdOptionValidation(t *testing.T) {
	for _, threshold := range []int{-1, 4097} {
		if _, err := NewSession(Options{
			DedupStrategy:        DedupStrategyCoverageParameters,
			PathSegmentThreshold: threshold,
		}); err == nil {
			t.Fatalf("threshold=%d accepted, want ErrInvalidOptions", threshold)
		}
	}
}

func TestPathSegmentThresholdParameterLocationEmitted(t *testing.T) {
	session, err := NewSession(Options{
		DedupStrategy:        DedupStrategyCoverageParameters,
		PathSegmentThreshold: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	reserveAndCommit(t, session, "https://example.test/a/1")
	reserveAndCommit(t, session, "https://example.test/a/2")
	// The folded segment must appear as a path parameter in extracted state.
	decision, _, err := session.Reserve(Request{URL: "https://example.test/a/3", BodyCaptured: true})
	if err != nil {
		t.Fatal(err)
	}
	if decision.ParameterStatus != ParameterStatusComplete {
		t.Fatalf("ParameterStatus = %s, want complete", decision.ParameterStatus)
	}
}
