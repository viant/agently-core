package maintenance

import "time"

// Mode controls whether a cleanup policy is disabled,
// validates and reports candidates, or executes its configured mutations.
type Mode string

const (
	ModeOff     Mode = "off"
	ModeDryRun  Mode = "dry-run"
	ModeExecute Mode = "execute"
)

func (m Mode) Enabled() bool {
	return m == ModeDryRun || m == ModeExecute
}

func (m Mode) Executes() bool {
	return m == ModeExecute
}

// Options controls the maintenance worker and its policies.
// Cleanup passes are coordinated across application instances by a database
// maintenance lease. A local guard additionally prevents in-process overlap.
type Options struct {
	Enabled              bool
	Interval             time.Duration
	BatchSize            int
	Timeout              time.Duration
	RunOnStart           bool
	Debug                bool
	InteractiveMode      Mode
	InteractiveRetention time.Duration
	ScheduledMode        Mode
	ScheduledRetention   time.Duration
	OrphanMode           Mode
	OrphanMinAge         time.Duration
}
