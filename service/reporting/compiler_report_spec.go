package reporting

import (
	shared "github.com/viant/forge/backend/reporting/engine"
	"time"
)

// ReportSpecCompiler is the shared canonical report compiler. Core retains
// export jobs and application persistence, not a second report-spec parser.
type ReportSpecCompiler = shared.ReportSpecCompiler

func NewReportSpecCompiler(now func() time.Time) *ReportSpecCompiler {
	return shared.NewReportSpecCompiler(now)
}
