package requestfilter

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionReservationLifecycle(t *testing.T) {
	session, err := NewSession(Options{DedupStrategy: DedupStrategySelectedFieldsHash})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	request := Request{URL: "https://example.test/a"}
	decision, reservation, err := session.Reserve(request)
	if err != nil || decision.Action != ActionAllow || decision.Reason != ReasonNewRequest {
		t.Fatalf("Reserve(first) = (%#v, %v)", decision, err)
	}
	decision, _, err = session.Reserve(request)
	if err != nil || decision.Action != ActionRetry || decision.Reason != ReasonInFlight {
		t.Fatalf("Reserve(in-flight) = (%#v, %v)", decision, err)
	}
	if err := session.Release(reservation); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	decision, reservation, err = session.Reserve(request)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(after release) = (%#v, %v)", decision, err)
	}
	if err := session.Commit(reservation); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if err := session.Commit(reservation); !errors.Is(err, ErrReservationNotFound) {
		t.Fatalf("Commit(reused token) error = %v, want ErrReservationNotFound", err)
	}
	decision, _, err = session.Reserve(request)
	if err != nil || decision.Action != ActionDrop || decision.Reason != ReasonExactDuplicate {
		t.Fatalf("Reserve(after commit) = (%#v, %v)", decision, err)
	}
}

func TestSessionIsolationAndClose(t *testing.T) {
	first, err := NewSession(Options{DedupStrategy: DedupStrategySelectedFieldsHash})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSession(Options{DedupStrategy: DedupStrategySelectedFieldsHash})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{URL: "https://example.test/a"}
	_, token, err := first.Reserve(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Commit(token); !errors.Is(err, ErrReservationNotFound) {
		t.Fatalf("Commit(foreign token) error = %v, want ErrReservationNotFound", err)
	}
	decision, _, err := second.Reserve(request)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(isolated session) = (%#v, %v)", decision, err)
	}
	first.Close()
	if _, _, err := first.Reserve(request); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Reserve(closed session) error = %v, want ErrSessionClosed", err)
	}
	if err := first.Release(token); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Release(closed session) error = %v, want ErrSessionClosed", err)
	}
	second.Close()
}

func TestSessionCapacityAndRelease(t *testing.T) {
	session, err := NewSession(Options{MaxEntries: 3, DedupStrategy: DedupStrategyParameterShapeCoverage})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	first := Request{URL: "https://example.test/a?x=1", BodyCaptured: true}
	decision, reservation, err := session.Reserve(first)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(first) = (%#v, %v)", decision, err)
	}
	if err := session.Release(reservation); err != nil {
		t.Fatal(err)
	}
	decision, reservation, err = session.Reserve(first)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(after release) = (%#v, %v)", decision, err)
	}
	if err := session.Commit(reservation); err != nil {
		t.Fatal(err)
	}
	decision, _, err = session.Reserve(Request{URL: "https://example.test/a?x=1&y=2", BodyCaptured: true})
	if err != nil || decision.Action != ActionReject || decision.Reason != ReasonCapacity {
		t.Fatalf("Reserve(over capacity) = (%#v, %v), want capacity rejection", decision, err)
	}
}

func TestSessionReservationExpiryAllowsRetry(t *testing.T) {
	session, err := NewSession(Options{ReservationTTL: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	request := Request{URL: "https://example.test/expire"}
	_, reservation, err := session.Reserve(request)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(25 * time.Millisecond)
	if err := session.Commit(reservation); !errors.Is(err, ErrReservationNotFound) {
		t.Fatalf("Commit(expired token) error = %v, want ErrReservationNotFound", err)
	}
	decision, _, err := session.Reserve(request)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(after expiry) = (%#v, %v)", decision, err)
	}
}

func TestSessionEntryTTL(t *testing.T) {
	session, err := NewSession(Options{EntryTTL: 10 * time.Millisecond, DedupStrategy: DedupStrategySelectedFieldsHash})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	request := Request{URL: "https://example.test/ttl"}
	_, reservation, err := session.Reserve(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(reservation); err != nil {
		t.Fatal(err)
	}
	time.Sleep(25 * time.Millisecond)
	decision, _, err := session.Reserve(request)
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(after entry expiry) = (%#v, %v)", decision, err)
	}
}

func TestConcurrentReserveIsAtomic(t *testing.T) {
	session, err := NewSession(Options{DedupStrategy: DedupStrategySelectedFieldsHash})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	const workers = 32
	var group sync.WaitGroup
	group.Add(workers)
	var mu sync.Mutex
	allowCount := 0
	retryCount := 0
	var reservation Reservation
	for range workers {
		go func() {
			defer group.Done()
			decision, token, err := session.Reserve(Request{URL: "https://example.test/concurrent"})
			if err != nil {
				t.Errorf("Reserve() error = %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			switch decision.Action {
			case ActionAllow:
				allowCount++
				reservation = token
			case ActionRetry:
				retryCount++
			default:
				t.Errorf("Reserve() action = %q", decision.Action)
			}
		}()
	}
	group.Wait()
	if allowCount != 1 || retryCount != workers-1 {
		t.Fatalf("allow/retry counts = %d/%d, want 1/%d", allowCount, retryCount, workers-1)
	}
	if err := session.Commit(reservation); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentUniqueRequestsRespectCapacity(t *testing.T) {
	const capacity = 64
	const requestCount = 128
	session, err := NewSession(Options{
		MaxEntries: capacity, DedupStrategy: DedupStrategySelectedFieldsHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	var group sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	rejected := 0
	group.Add(requestCount)
	for index := range requestCount {
		go func() {
			defer group.Done()
			request := Request{URL: fmt.Sprintf("https://example.test/item/%d", index)}
			decision, reservation, reserveErr := session.Reserve(request)
			if reserveErr != nil {
				t.Errorf("Reserve() error = %v", reserveErr)
				return
			}
			if decision.Action == ActionAllow {
				if commitErr := session.Commit(reservation); commitErr != nil {
					t.Errorf("Commit() error = %v", commitErr)
					return
				}
			}
			mu.Lock()
			defer mu.Unlock()
			switch decision.Action {
			case ActionAllow:
				allowed++
			case ActionReject:
				if decision.Reason != ReasonCapacity {
					t.Errorf("Reserve() reject reason = %q, want capacity", decision.Reason)
				}
				rejected++
			default:
				t.Errorf("Reserve() action = %q, want allow or reject", decision.Action)
			}
		}()
	}
	group.Wait()
	if allowed != capacity || rejected != requestCount-capacity {
		t.Fatalf("allow/reject counts = %d/%d, want %d/%d", allowed, rejected, capacity, requestCount-capacity)
	}
}

func TestCoveragePendingRequestCanRetryAfterRelease(t *testing.T) {
	session, err := NewSession(Options{DedupStrategy: DedupStrategyParameterShapeCoverage})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	first := Request{URL: "https://example.test/a?x=1", BodyCaptured: true}
	_, reservation, err := session.Reserve(first)
	if err != nil {
		t.Fatal(err)
	}
	decision, _, err := session.Reserve(Request{URL: "https://example.test/a?x=1&y=2", BodyCaptured: true})
	if err != nil || decision.Action != ActionRetry || decision.Reason != ReasonInFlight {
		t.Fatalf("Reserve(overlapping pending coverage) = (%#v, %v)", decision, err)
	}
	if err := session.Release(reservation); err != nil {
		t.Fatal(err)
	}
	decision, retryToken, err := session.Reserve(Request{URL: "https://example.test/a?x=1&y=2"})
	if err != nil || decision.Action != ActionAllow {
		t.Fatalf("Reserve(after failed request release) = (%#v, %v)", decision, err)
	}
	if err := session.Commit(retryToken); err != nil {
		t.Fatal(err)
	}
}

func TestSensitiveValuesAreNotReturned(t *testing.T) {
	const secret = "secret-token-value"
	result, err := FilterBatch([]Request{{
		URL:                   "https://example.test/?token=" + secret,
		Header:                http.Header{"Cookie": {"session=" + secret}},
		AuthenticationGroupID: secret,
	}}, Options{DedupStrategy: DedupStrategySelectedFieldsHash})
	if err != nil {
		t.Fatal(err)
	}
	decision := result.Decisions[0]
	if strings.Contains(decision.Fingerprint, secret) {
		t.Fatalf("fingerprint leaked sensitive value: %q", decision.Fingerprint)
	}
	_, err = FilterBatch([]Request{{URL: "https://user:" + secret + "@example.test/"}}, Options{})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("invalid URL error = %v, want a generic error without credentials", err)
	}
	if result.Accepted[0].URL == "" || result.Accepted[0].Header.Get("Cookie") == "" {
		t.Fatal("accepted request unexpectedly lost caller data")
	}
}
