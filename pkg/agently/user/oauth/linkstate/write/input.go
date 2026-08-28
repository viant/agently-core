package write

// Input carries the requested pending flow row. The current row registered
// under the same flow hash (the cross-pod deduplication anchor) is loaded by
// the handler inside its own CAS sequence: a bound view parameter would both
// race with the body binding and observe a row older than the transition it
// guards.
type Input struct {
	State *LinkState `parameter:",kind=body,in=data"`
}
