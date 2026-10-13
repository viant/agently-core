package writekeys

// Flow is the narrow key row used to scope the link-state Current read.
type Flow struct {
	FlowHash string `sqlx:"flow_hash"`
}
