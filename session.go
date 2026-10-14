package requestfilter

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// Session stores bounded deduplication state for one input stream.
type Session struct {
	mu           sync.Mutex
	id           [16]byte
	options      normalizedOptions
	closed       bool
	nextID       uint64
	stateEntries int
	exact        map[string]time.Time
	pendingExact map[string]uint64
	pending      map[uint64]*reservationState
	coverage     map[string]*coverageState
}

type reservationState struct {
	fingerprint   string
	endpointKey   string
	parameterIDs  []string
	trackCoverage bool
	expiresAt     time.Time
}

type coverageState struct {
	seen             bool
	lastSeen         time.Time
	parameters       map[string]time.Time
	pendingParams    map[string]map[uint64]struct{}
	pendingEndpoints map[uint64]struct{}
}

// NewSession creates an isolated, concurrency-safe request filter session.
func NewSession(options Options) (*Session, error) {
	normalized, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	session := &Session{
		options: normalized,
		exact:   make(map[string]time.Time), pendingExact: make(map[string]uint64),
		pending: make(map[uint64]*reservationState), coverage: make(map[string]*coverageState),
	}
	if _, err := rand.Read(session.id[:]); err != nil {
		return nil, fmt.Errorf("requestfilter: initialize session: %w", err)
	}
	return session, nil
}

// Reserve evaluates a request and reserves its state until Commit or Release.
func (session *Session) Reserve(request Request) (Decision, Reservation, error) {
	normalized, err := normalizeRequest(request, session.options)
	if err != nil {
		return Decision{}, Reservation{}, err
	}
	if session.options.ParameterPolicy != ParameterPolicyNone {
		normalized.parameters, normalized.parameterStatus, err = extractParameters(normalized, session.options)
		if err != nil {
			return Decision{}, Reservation{}, err
		}
	}

	now := time.Now()
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return Decision{}, Reservation{}, ErrSessionClosed
	}
	session.expireLocked(now)
	decision := Decision{
		Action: ActionAllow, Reason: ReasonNewRequest,
		Fingerprint: normalized.fingerprint, ParameterStatus: normalized.parameterStatus,
	}
	trackCoverage := session.options.ParameterPolicy == ParameterPolicyCoverage &&
		normalized.parameterStatus == ParameterStatusComplete
	var coverage *coverageState
	if trackCoverage {
		coverage = session.coverage[normalized.endpointKey]
	}
	parameterIDs := make([]string, len(normalized.parameters))
	if trackCoverage {
		for index, parameter := range normalized.parameters {
			parameterIDs[index] = parameterID(parameter)
		}
	}
	if _, exists := session.exact[normalized.fingerprint]; exists {
		session.exact[normalized.fingerprint] = now
		if coverage != nil {
			session.touchCoverageLocked(coverage, parameterIDs, now)
		}
		decision.Action, decision.Reason = ActionDrop, ReasonExactDuplicate
		return decision, Reservation{}, nil
	}
	if _, exists := session.pendingExact[normalized.fingerprint]; exists {
		decision.Action, decision.Reason = ActionRetry, ReasonInFlight
		return decision, Reservation{}, nil
	}
	if session.options.ParameterPolicy == ParameterPolicyIgnoreEmpty &&
		normalized.parameterStatus == ParameterStatusComplete && len(normalized.parameters) == 0 {
		decision.Action, decision.Reason = ActionDrop, ReasonNoParameters
		return decision, Reservation{}, nil
	}

	if session.options.ParameterPolicy == ParameterPolicyCoverage && trackCoverage {
		if action, reason := session.coverageDecisionLocked(coverage, parameterIDs, now); action != ActionAllow {
			decision.Action, decision.Reason = action, reason
			return decision, Reservation{}, nil
		}
	}
	if trackCoverage {
		if coverage != nil {
			session.touchCoverageLocked(coverage, parameterIDs, now)
		}
	}

	newEntries := 1
	if trackCoverage {
		if coverage == nil {
			newEntries++
		}
		newEntries += len(parameterIDs)
	}
	if session.stateEntries+newEntries > session.options.MaxEntries {
		decision.Action, decision.Reason = ActionReject, ReasonCapacity
		return decision, Reservation{}, nil
	}

	session.nextID++
	if session.nextID == 0 {
		session.nextID++
	}
	id := session.nextID
	reservation := Reservation{sessionID: session.id, id: id}
	state := &reservationState{
		fingerprint:   normalized.fingerprint,
		endpointKey:   normalized.endpointKey,
		trackCoverage: trackCoverage,
		expiresAt:     now.Add(session.options.ReservationTTL),
	}
	session.pending[id] = state
	session.pendingExact[normalized.fingerprint] = id
	session.stateEntries++
	if trackCoverage {
		if coverage == nil {
			coverage = newCoverageState()
			session.coverage[normalized.endpointKey] = coverage
			session.stateEntries++
		}
		if !coverage.seen {
			coverage.pendingEndpoints[id] = struct{}{}
		}
		for _, parameterID := range parameterIDs {
			owners := coverage.pendingParams[parameterID]
			if owners == nil {
				owners = make(map[uint64]struct{})
				coverage.pendingParams[parameterID] = owners
			}
			owners[id] = struct{}{}
			state.parameterIDs = append(state.parameterIDs, parameterID)
			session.stateEntries++
		}
	}
	decision.Action = ActionAllow
	return decision, reservation, nil
}

// Commit records a request after the downstream system accepts it.
func (session *Session) Commit(reservation Reservation) error {
	now := time.Now()
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return ErrSessionClosed
	}
	session.expireLocked(now)
	state, ok := session.reservationLocked(reservation)
	if !ok {
		return ErrReservationNotFound
	}
	session.finishPendingLocked(reservation.id, state)
	session.exact[state.fingerprint] = now
	if state.trackCoverage {
		coverage := session.coverage[state.endpointKey]
		coverage.seen = true
		coverage.lastSeen = now
		for _, parameterID := range state.parameterIDs {
			delete(coverage.pendingParams[parameterID], reservation.id)
			if len(coverage.pendingParams[parameterID]) == 0 {
				delete(coverage.pendingParams, parameterID)
			}
			session.stateEntries--
			if _, exists := coverage.parameters[parameterID]; !exists {
				coverage.parameters[parameterID] = now
				session.stateEntries++
			} else {
				coverage.parameters[parameterID] = now
			}
		}
		delete(coverage.pendingEndpoints, reservation.id)
	}
	return nil
}

// Release removes a reservation so the caller can retry a failed request.
func (session *Session) Release(reservation Reservation) error {
	now := time.Now()
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return ErrSessionClosed
	}
	session.expireLocked(now)
	state, ok := session.reservationLocked(reservation)
	if !ok {
		return ErrReservationNotFound
	}
	session.releaseLocked(reservation.id, state)
	return nil
}

// Close releases all session state and prevents further operations.
func (session *Session) Close() {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.closed = true
	session.stateEntries = 0
	clear(session.exact)
	clear(session.pendingExact)
	clear(session.pending)
	clear(session.coverage)
}

func (session *Session) coverageDecisionLocked(coverage *coverageState, parameterIDs []string, now time.Time) (Action, Reason) {
	if coverage == nil {
		return ActionAllow, ReasonNewRequest
	}
	if len(parameterIDs) == 0 {
		if coverage.seen {
			coverage.lastSeen = now
			return ActionDrop, ReasonParameterCoverage
		}
		if len(coverage.pendingEndpoints) > 0 {
			return ActionRetry, ReasonInFlight
		}
		return ActionAllow, ReasonNewRequest
	}
	allCommitted := true
	for _, id := range parameterIDs {
		if _, committed := coverage.parameters[id]; committed {
			continue
		}
		allCommitted = false
		if len(coverage.pendingParams[id]) > 0 {
			return ActionRetry, ReasonInFlight
		}
	}
	if allCommitted && coverage.seen {
		coverage.lastSeen = now
		for _, id := range parameterIDs {
			coverage.parameters[id] = now
		}
		return ActionDrop, ReasonParameterCoverage
	}
	return ActionAllow, ReasonNewRequest
}

func (session *Session) touchCoverageLocked(coverage *coverageState, parameterIDs []string, now time.Time) {
	if coverage.seen {
		coverage.lastSeen = now
	}
	for _, id := range parameterIDs {
		if _, exists := coverage.parameters[id]; exists {
			coverage.parameters[id] = now
		}
	}
}

func (session *Session) reservationLocked(reservation Reservation) (*reservationState, bool) {
	if reservation.sessionID != session.id || reservation.id == 0 {
		return nil, false
	}
	state, exists := session.pending[reservation.id]
	return state, exists
}

func (session *Session) finishPendingLocked(id uint64, state *reservationState) {
	delete(session.pending, id)
	delete(session.pendingExact, state.fingerprint)
}

func (session *Session) releaseLocked(id uint64, state *reservationState) {
	session.finishPendingLocked(id, state)
	session.stateEntries--
	if !state.trackCoverage {
		return
	}
	coverage := session.coverage[state.endpointKey]
	if coverage == nil {
		return
	}
	for _, parameterID := range state.parameterIDs {
		delete(coverage.pendingParams[parameterID], id)
		if len(coverage.pendingParams[parameterID]) == 0 {
			delete(coverage.pendingParams, parameterID)
		}
		session.stateEntries--
	}
	delete(coverage.pendingEndpoints, id)
	if !coverage.seen && len(coverage.parameters) == 0 && len(coverage.pendingParams) == 0 && len(coverage.pendingEndpoints) == 0 {
		delete(session.coverage, state.endpointKey)
		session.stateEntries--
	}
}

func (session *Session) expireLocked(now time.Time) {
	for id, state := range session.pending {
		if !state.expiresAt.After(now) {
			session.releaseLocked(id, state)
		}
	}
	if session.options.EntryTTL == 0 {
		return
	}
	for fingerprint, lastSeen := range session.exact {
		if now.Sub(lastSeen) > session.options.EntryTTL {
			delete(session.exact, fingerprint)
			session.stateEntries--
		}
	}
	for endpoint, coverage := range session.coverage {
		for id, lastSeen := range coverage.parameters {
			if now.Sub(lastSeen) > session.options.EntryTTL && len(coverage.pendingParams[id]) == 0 {
				delete(coverage.parameters, id)
				session.stateEntries--
			}
		}
		if coverage.seen && now.Sub(coverage.lastSeen) > session.options.EntryTTL {
			coverage.seen = false
		}
		if !coverage.seen && len(coverage.parameters) == 0 && len(coverage.pendingParams) == 0 && len(coverage.pendingEndpoints) == 0 {
			delete(session.coverage, endpoint)
			session.stateEntries--
		}
	}
}

func newCoverageState() *coverageState {
	return &coverageState{
		parameters:       make(map[string]time.Time),
		pendingParams:    make(map[string]map[uint64]struct{}),
		pendingEndpoints: make(map[uint64]struct{}),
	}
}

func parameterID(parameter Parameter) string {
	return digestFields(string(parameter.Location), parameter.Name)
}

func cloneRequest(request Request) Request {
	clone := request
	clone.Body = append([]byte(nil), request.Body...)
	clone.Header = request.Header.Clone()
	if request.Header == nil {
		clone.Header = nil
	}
	return clone
}
