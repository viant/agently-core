package reporting

import (
	"errors"
	"testing"

	reportjob "github.com/viant/agently-core/model/reportjob"
)

func TestRunExportCandidateCarriesIdentityButNoCallerSnapshot(t *testing.T) {
	base := reportjob.Record{JobID: "job-1", OwnerID: "alice", ConversationID: "conv-1", ReportRunID: "run-1", ExportRequestID: "request-1", ArtifactRef: "report-run://run-1", Format: "pdf", Scope: "draft", Status: "queued"}
	if err := ValidateRunExportCandidate(&base); err != nil {
		t.Fatalf("exact run reference rejected: %v", err)
	}
	for _, test := range []struct {
		name string
		set  func(*reportjob.Record)
	}{
		{"missing job", func(job *reportjob.Record) { job.JobID = "" }},
		{"missing owner", func(job *reportjob.Record) { job.OwnerID = "" }},
		{"spec snapshot", func(job *reportjob.Record) { job.ReportSpec = []byte(`{}`) }},
		{"fill snapshot", func(job *reportjob.Record) { job.ReportFill = []byte(`{}`) }},
		{"print snapshot", func(job *reportjob.Record) { job.ReportPrint = []byte(`{}`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			test.set(&candidate)
			if err := ValidateRunExportCandidate(&candidate); !errors.Is(err, ErrConflict) {
				t.Fatalf("mixed or incomplete run candidate accepted: %v", err)
			}
		})
	}
}
