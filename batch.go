package requestfilter

// FilterBatch filters requests in input order using a temporary session.
func FilterBatch(requests []Request, options Options) (BatchResult, error) {
	session, err := NewSession(options)
	if err != nil {
		return BatchResult{}, err
	}
	defer session.Close()
	result := BatchResult{
		Accepted:  make([]Request, 0, len(requests)),
		Decisions: make([]Decision, 0, len(requests)),
	}
	for _, request := range requests {
		decision, reservation, err := session.Reserve(request)
		if err != nil {
			return BatchResult{}, err
		}
		if decision.Action == ActionAllow {
			if err := session.Commit(reservation); err != nil {
				return BatchResult{}, err
			}
			result.Accepted = append(result.Accepted, cloneRequest(request))
		}
		result.Decisions = append(result.Decisions, decision)
	}
	return result, nil
}
