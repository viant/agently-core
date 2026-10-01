package run

// RunPatchCondition carries expected values for an atomic sparse run update.
// The canonical generated writer evaluates these criteria in the database.
type RunPatchCondition struct {
	// Status is the expected current run status (required for claim shapes).
	Status string `json:",omitempty"`
	// LeaseOwner is the expected current lease owner. nil means "no owner
	// criterion" (legacy rows with NULL lease_owner); it must be combined
	// with Status and Attempt.
	LeaseOwner *string `json:",omitempty"`
	// Attempt is the expected current attempt.
	Attempt *int `json:",omitempty"`
}
