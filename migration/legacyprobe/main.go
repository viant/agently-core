// legacyprobe is an isolated parity-test executable linked to the unchanged v0 SDK.
// It uses legacy components; schema setup/seed data are owned by the test harness.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	reportingsql "github.com/viant/agently-core/app/store/reporting/sql"
	authctx "github.com/viant/agently-core/internal/auth"
	conversationread "github.com/viant/agently-core/pkg/agently/conversation"
	conversationlist "github.com/viant/agently-core/pkg/agently/conversation/list"
	conversationwrite "github.com/viant/agently-core/pkg/agently/conversation/write"
	fileread "github.com/viant/agently-core/pkg/agently/generatedfile/read"
	filewrite "github.com/viant/agently-core/pkg/agently/generatedfile/write"
	goal "github.com/viant/agently-core/pkg/agently/goal"
	writer "github.com/viant/agently-core/pkg/agently/goal/write"
	messageget "github.com/viant/agently-core/pkg/agently/message"
	messageelicitation "github.com/viant/agently-core/pkg/agently/message/elicitation"
	messagecount "github.com/viant/agently-core/pkg/agently/message/elicitationCount"
	messagelist "github.com/viant/agently-core/pkg/agently/message/list"
	messagelookup "github.com/viant/agently-core/pkg/agently/message/read"
	messagewrite "github.com/viant/agently-core/pkg/agently/message/write"
	modelcallwrite "github.com/viant/agently-core/pkg/agently/modelcall/write"
	payload "github.com/viant/agently-core/pkg/agently/payload"
	payloadread "github.com/viant/agently-core/pkg/agently/payload/read"
	payloadwrite "github.com/viant/agently-core/pkg/agently/payload/write"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	sharedrecord "github.com/viant/agently-core/pkg/agently/reportshareartifact"
	runread "github.com/viant/agently-core/pkg/agently/run"
	runactive "github.com/viant/agently-core/pkg/agently/run/active"
	runstale "github.com/viant/agently-core/pkg/agently/run/stale"
	runsteps "github.com/viant/agently-core/pkg/agently/run/steps"
	runwrite "github.com/viant/agently-core/pkg/agently/run/write"
	schedrun "github.com/viant/agently-core/pkg/agently/scheduler/run"
	runlease "github.com/viant/agently-core/pkg/agently/scheduler/run/lease"
	scheduleread "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	scheduledelete "github.com/viant/agently-core/pkg/agently/scheduler/schedule/delete"
	schedulelease "github.com/viant/agently-core/pkg/agently/scheduler/schedule/lease"
	schedulewrite "github.com/viant/agently-core/pkg/agently/scheduler/schedule/write"
	approvalcount "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/count"
	approvaloutcome "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/outcome"
	approvalpending "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/pendingCount"
	approvalread "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	approvalwrite "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/write"
	toolbyop "github.com/viant/agently-core/pkg/agently/toolcall/byOp"
	toolbyturn "github.com/viant/agently-core/pkg/agently/toolcall/byTurn"
	toolread "github.com/viant/agently-core/pkg/agently/toolcall/read"
	toolcallwrite "github.com/viant/agently-core/pkg/agently/toolcall/write"
	turnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	turnlookup "github.com/viant/agently-core/pkg/agently/turn/byId"
	turncontroller "github.com/viant/agently-core/pkg/agently/turn/controllerCount"
	turnlist "github.com/viant/agently-core/pkg/agently/turn/list"
	turnnext "github.com/viant/agently-core/pkg/agently/turn/nextQueued"
	turncount "github.com/viant/agently-core/pkg/agently/turn/queuedCount"
	turnqueued "github.com/viant/agently-core/pkg/agently/turn/queuedList"
	turnwrite "github.com/viant/agently-core/pkg/agently/turn/write"
	queueread "github.com/viant/agently-core/pkg/agently/turnqueue/read"
	queuewrite "github.com/viant/agently-core/pkg/agently/turnqueue/write"
	userread "github.com/viant/agently-core/pkg/agently/user"
	oauthread "github.com/viant/agently-core/pkg/agently/user/oauth"
	linkread "github.com/viant/agently-core/pkg/agently/user/oauth/linkstate"
	linkconsume "github.com/viant/agently-core/pkg/agently/user/oauth/linkstate/consume"
	linkcleanup "github.com/viant/agently-core/pkg/agently/user/oauth/linkstate/deleteexpired"
	linkwrite "github.com/viant/agently-core/pkg/agently/user/oauth/linkstate/write"
	oauthwrite "github.com/viant/agently-core/pkg/agently/user/oauth/write"
	sessionread "github.com/viant/agently-core/pkg/agently/user/session"
	sessiondelete "github.com/viant/agently-core/pkg/agently/user/session/delete"
	sessionwrite "github.com/viant/agently-core/pkg/agently/user/session/write"
	userwrite "github.com/viant/agently-core/pkg/agently/user/write"
	forgeread "github.com/viant/agently-core/pkg/forge/reporting"
	forgelist "github.com/viant/agently-core/pkg/forge/reporting/list"
	forgewrite "github.com/viant/agently-core/pkg/forge/reporting/write"
	"github.com/viant/datly"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/datly/view"
	hstate "github.com/viant/xdatly/handler/state"
	_ "modernc.org/sqlite"
	"reflect"
)

type request struct {
	Principal               string
	Selectors               []*hstate.NamedQuerySelector
	Component               string
	Filters                 map[string]json.RawMessage
	Raw                     bool
	DBPath                  string
	Body                    string
	Method                  string
	SeedExisting            bool
	ExpectedRevision        int64
	ExpectedContextRevision int64
	ConversationIDs         []string
}
type result struct {
	Failed bool
	Error  string
	Rows   []json.RawMessage
	Output json.RawMessage
}

func main() {
	protocolOutput := os.Stdout
	os.Stdout = os.Stderr // Keep legacy library diagnostics out of the JSON probe protocol.
	var in request
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		panic(err)
	}
	ctx := context.Background()
	if in.Principal != "" {
		ctx = authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: in.Principal})
	}
	dao, err := datly.New(ctx)
	must(err)
	must(dao.AddConnectors(ctx, view.NewConnector("agently", "sqlite", "file:"+in.DBPath+"?_pragma=foreign_keys(1)")))
	if in.Component == "toolCallReader" {
		must(json.NewEncoder(protocolOutput).Encode(runToolCallReader(ctx, dao, in)))
		return
	}
	if in.Component == "toolCall" {
		must(json.NewEncoder(protocolOutput).Encode(runToolCall(ctx, dao, in)))
		return
	}
	if in.Component == "modelCallUsage" {
		must(json.NewEncoder(protocolOutput).Encode(runModelCallUsage(ctx, dao, in)))
		return
	}
	if in.Component == "modelCallTranscript" {
		must(json.NewEncoder(protocolOutput).Encode(runModelCallTranscript(ctx, dao, in)))
		return
	}
	if in.Component == "approvalReader" {
		must(json.NewEncoder(protocolOutput).Encode(runApprovalReader(ctx, dao, in)))
		return
	}
	if in.Component == "approval" {
		must(json.NewEncoder(protocolOutput).Encode(runApproval(ctx, dao, in)))
		return
	}
	if in.Component == "messageReader" {
		must(json.NewEncoder(protocolOutput).Encode(runMessageReader(ctx, dao, in)))
		return
	}
	if in.Component == "message" {
		protocolResult := runMessage(ctx, dao, in)
		must(json.NewEncoder(protocolOutput).Encode(protocolResult))
		return
	}
	if in.Component == "modelCall" {
		must(json.NewEncoder(protocolOutput).Encode(runModelCall(ctx, dao, in)))
		return
	}
	if in.Component == "turn" {
		must(json.NewEncoder(protocolOutput).Encode(runTurn(ctx, dao, in)))
		return
	}
	if in.Component == "turnReader" {
		must(json.NewEncoder(protocolOutput).Encode(runTurnReader(ctx, dao, in)))
		return
	}
	if in.Component == "forgeWriter" {
		must(json.NewEncoder(protocolOutput).Encode(runForgeWriter(ctx, dao, in)))
		return
	}
	if in.Component == "forgeStore" {
		must(json.NewEncoder(protocolOutput).Encode(runForgeStore(ctx, dao, in)))
		return
	}
	if in.Component == "reportContext" {
		must(json.NewEncoder(protocolOutput).Encode(runReportContext(ctx, dao, in)))
		return
	}
	if in.Component == "reportRun" {
		must(json.NewEncoder(protocolOutput).Encode(runReportRun(ctx, dao, in)))
		return
	}
	if in.Component == "reportAdoption" {
		must(json.NewEncoder(protocolOutput).Encode(runReportAdoption(ctx, dao, in)))
		return
	}
	if in.Component == "reportJob" {
		must(json.NewEncoder(protocolOutput).Encode(runReportJob(ctx, dao, in)))
		return
	}
	if in.Component == "reportArtifact" {
		must(json.NewEncoder(protocolOutput).Encode(runReportArtifact(ctx, dao, in)))
		return
	}
	if in.Component == "reportComplete" {
		must(json.NewEncoder(protocolOutput).Encode(runReportComplete(ctx, dao, in)))
		return
	}
	if in.Component == "forgeReader" {
		must(json.NewEncoder(protocolOutput).Encode(runForgeReader(ctx, dao, in)))
		return
	}
	if in.Component == "runSteps" {
		must(json.NewEncoder(protocolOutput).Encode(runStepsReader(ctx, dao, in)))
		return
	}
	if in.Component == "runActive" || in.Component == "runStale" {
		must(json.NewEncoder(protocolOutput).Encode(runRunVariant(ctx, dao, in)))
		return
	}
	if in.Component == "run" {
		must(json.NewEncoder(protocolOutput).Encode(runRun(ctx, dao, in)))
		return
	}
	if in.Component == "schedulerRuns" || in.Component == "schedulerRunDue" {
		must(json.NewEncoder(protocolOutput).Encode(runSchedulerRuns(ctx, dao, in)))
		return
	}
	if in.Component == "schedulerRunTotal" {
		must(json.NewEncoder(protocolOutput).Encode(runSchedulerRunTotal(ctx, dao, in)))
		return
	}
	if in.Component == "schedulerRunList" {
		must(json.NewEncoder(protocolOutput).Encode(runSchedulerRunList(ctx, dao, in)))
		return
	}
	if in.Component == "runLease" {
		must(json.NewEncoder(protocolOutput).Encode(runRunLease(ctx, dao, in)))
		return
	}
	if in.Component == "scheduleLease" {
		must(json.NewEncoder(protocolOutput).Encode(runScheduleLease(ctx, dao, in)))
		return
	}
	if in.Component == "schedule" || in.Component == "scheduleDelete" {
		must(json.NewEncoder(protocolOutput).Encode(runSchedule(ctx, dao, in)))
		return
	}
	if in.Component == "turnQueue" {
		must(json.NewEncoder(protocolOutput).Encode(runTurnQueue(ctx, dao, in)))
		return
	}
	if in.Component == "queueReorder" {
		must(json.NewEncoder(protocolOutput).Encode(runQueueReorder(ctx, dao)))
		return
	}
	if in.Component == "conversation" {
		must(json.NewEncoder(protocolOutput).Encode(runConversation(ctx, dao, in)))
		return
	}
	if in.Component == "conversationList" {
		must(json.NewEncoder(protocolOutput).Encode(runConversationList(ctx, dao, in)))
		return
	}
	if in.Component == "linkStateCleanup" {
		must(json.NewEncoder(protocolOutput).Encode(runLinkStateCleanup(ctx, dao, in)))
		return
	}
	if in.Component == "linkStateConsume" {
		must(json.NewEncoder(protocolOutput).Encode(runLinkStateConsume(ctx, dao, in)))
		return
	}
	if in.Component == "linkStateReader" {
		must(json.NewEncoder(protocolOutput).Encode(runLinkStateReader(ctx, dao, in)))
		return
	}
	if in.Component == "linkStateWrite" {
		must(json.NewEncoder(protocolOutput).Encode(runLinkStateWrite(ctx, dao, in)))
		return
	}
	if in.Component == "oauthToken" {
		must(json.NewEncoder(protocolOutput).Encode(runOAuthToken(ctx, dao, in)))
		return
	}
	if in.Component == "session" || in.Component == "sessionDelete" {
		must(json.NewEncoder(protocolOutput).Encode(runSession(ctx, dao, in)))
		return
	}
	if in.Component == "user" {
		must(json.NewEncoder(protocolOutput).Encode(runUser(ctx, dao, in)))
		return
	}
	if in.Component == "generatedFile" {
		must(json.NewEncoder(protocolOutput).Encode(runGeneratedFile(ctx, dao, in)))
		return
	}
	if in.Component == "payload" {
		actual := runPayload(ctx, dao, in)
		must(json.NewEncoder(protocolOutput).Encode(actual))
		return
	}
	must(goal.DefineGoalComponent(ctx, dao))
	_, err = writer.DefineComponent(ctx, dao)
	must(err)
	_, err = writer.DefineDeleteComponent(ctx, dao)
	must(err)
	actual := result{Rows: []json.RawMessage{}}
	if in.Body != "" {
		method := in.Method
		if method == "" {
			method = "PATCH"
		}
		req := httptest.NewRequest(method, writer.PathURI, strings.NewReader(in.Body))
		req.Header.Set("Content-Type", "application/json")
		if method == "DELETE" {
			output := &writer.DeleteOutput{}
			_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath(method, writer.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
			actual.Failed = err != nil || output.Status.Status == "error"
			if err != nil {
				actual.Error = err.Error()
			} else {
				actual.Error = output.Status.Message
			}
		} else {
			output := &writer.Output{}
			_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath(method, writer.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
			actual.Failed = err != nil || output.Status.Status == "error" || len(output.Violations) > 0
			if err != nil {
				actual.Error = err.Error()
			} else {
				actual.Error = output.Status.Message
			}
			actual.Output, _ = json.Marshal(output.Data)
		}
	}
	for _, id := range in.ConversationIDs {
		output := &goal.GoalOutput{}
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", goal.GoalPathURI)), datly.WithInput(&goal.GoalInput{ConversationID: id, Has: &goal.GoalInputHas{ConversationID: true}}), datly.WithOutput(output))
		must(err)
		for _, row := range output.Data {
			data, err := json.Marshal(row)
			must(err)
			actual.Rows = append(actual.Rows, data)
		}
	}
	must(json.NewEncoder(protocolOutput).Encode(actual))
}

func runTurn(ctx context.Context, dao *datly.Service, in request) result {
	must(turnlist.DefineTurnRowsComponent(ctx, dao))
	_, err := turnwrite.DefineComponent(ctx, dao)
	must(err)
	if in.Method == "DELETE" {
		_, err = turnwrite.DefineDeleteComponent(ctx, dao)
		must(err)
	}
	actual := result{Rows: []json.RawMessage{}}
	if in.Body != "" {
		if in.Method == "DELETE" {
			output := &turnwrite.DeleteOutput{}
			req := httptest.NewRequest("DELETE", turnwrite.PathURI, strings.NewReader(in.Body))
			req.Header.Set("Content-Type", "application/json")
			_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", turnwrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
			actual.Failed = err != nil || output.Status.Status == "error"
			if err != nil {
				actual.Error = err.Error()
			} else {
				actual.Error = output.Status.Message
			}
		} else {
			output := &turnwrite.Output{}
			req := httptest.NewRequest("PATCH", turnwrite.PathURI, strings.NewReader(in.Body))
			req.Header.Set("Content-Type", "application/json")
			_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", turnwrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
			actual.Failed = err != nil || output.Status.Status == "error" || len(output.Violations) > 0
			if err != nil {
				actual.Error = err.Error()
			} else {
				actual.Error = output.Status.Message
			}
			actual.Output, _ = json.Marshal(output.Data)
		}
	}
	input := &turnlist.TurnRowsInput{Has: &turnlist.TurnRowsInputHas{}}
	if raw, ok := in.Filters["conversationId"]; ok {
		must(json.Unmarshal(raw, &input.ConversationID))
		input.Has.ConversationID = true
	}
	output := &turnlist.TurnRowsOutput{}
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", turnlist.TurnRowsPathURI)), datly.WithInput(input), datly.WithOutput(output))
	must(err)
	for _, row := range output.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runTurnQueue(ctx context.Context, dao *datly.Service, in request) result {
	must(queueread.DefineQueueRowsComponent(ctx, dao))
	_, err := queuewrite.DefineComponent(ctx, dao)
	must(err)
	actual := result{Rows: []json.RawMessage{}}
	if in.Body != "" {
		out := &queuewrite.Output{}
		req := httptest.NewRequest("PATCH", queuewrite.PathURI, strings.NewReader(in.Body))
		req.Header.Set("Content-Type", "application/json")
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", queuewrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(out))
		actual.Failed = err != nil || out.Status.Status == "error" || len(out.Violations) > 0
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = out.Status.Message
		}
		actual.Output, _ = json.Marshal(out.Data)
	}
	input := &queueread.QueueRowsInput{Has: &queueread.QueueRowsInputHas{}}
	for _, field := range []struct {
		name    string
		value   *string
		present *bool
	}{
		{"id", &input.Id, &input.Has.Id}, {"conversationId", &input.ConversationId, &input.Has.ConversationId},
		{"turnId", &input.TurnId, &input.Has.TurnId}, {"messageId", &input.MessageId, &input.Has.MessageId}, {"status", &input.QueueStatus, &input.Has.QueueStatus},
	} {
		if raw, ok := in.Filters[field.name]; ok {
			must(json.Unmarshal(raw, field.value))
			*field.present = true
		}
	}
	out := &queueread.QueueRowsOutput{}
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", queueread.QueueRowsPathURI)), datly.WithInput(input), datly.WithOutput(out))
	must(err)
	for _, row := range out.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

// runQueueReorder reproduces the old embedded caller's persistence sequence:
// one turn batch followed by two independent turn_queue patches.
func runQueueReorder(ctx context.Context, dao *datly.Service) result {
	actual := result{Rows: []json.RawMessage{}}
	if _, err := turnwrite.DefineComponent(ctx, dao); err != nil {
		actual.Failed, actual.Error = true, err.Error()
		return actual
	}
	if _, err := queuewrite.DefineComponent(ctx, dao); err != nil {
		actual.Failed, actual.Error = true, err.Error()
		return actual
	}
	first, second := &turnwrite.Turn{}, &turnwrite.Turn{}
	first.SetId("t1")
	first.SetQueueSeq(1)
	second.SetId("t2")
	second.SetQueueSeq(2)
	turnOut := &turnwrite.Output{}
	_, err := dao.Operate(ctx,
		datly.WithPath(contract.NewPath("PATCH", turnwrite.PathURI)),
		datly.WithInput(&turnwrite.Input{Turns: []*turnwrite.Turn{first, second}}),
		datly.WithOutput(turnOut))
	if err == nil && turnOut.Status.Status == "error" {
		err = fmt.Errorf("%s", turnOut.Status.Message)
	}
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
		return actual
	}
	for _, item := range []struct {
		id  string
		seq int64
	}{{"q1", 1}, {"q2", 2}} {
		row := &queuewrite.TurnQueue{}
		row.SetId(item.id)
		row.SetQueueSeq(item.seq)
		out := &queuewrite.Output{}
		_, err = dao.Operate(ctx,
			datly.WithPath(contract.NewPath("PATCH", queuewrite.PathURI)),
			datly.WithInput(&queuewrite.Input{Queues: []*queuewrite.TurnQueue{row}}),
			datly.WithOutput(out))
		if err == nil && out.Status.Status == "error" {
			err = fmt.Errorf("%s", out.Status.Message)
		}
		if err != nil {
			actual.Failed, actual.Error = true, err.Error()
			return actual
		}
	}
	return actual
}

func runConversation(ctx context.Context, dao *datly.Service, in request) result {
	must(conversationread.DefineConversationComponent(ctx, dao))
	_, err := conversationwrite.DefineComponent(ctx, dao)
	must(err)
	actual := result{Rows: []json.RawMessage{}}
	if in.Body != "" {
		out := &conversationwrite.Output{}
		req := httptest.NewRequest("PATCH", conversationwrite.PathURI, strings.NewReader(in.Body))
		req.Header.Set("Content-Type", "application/json")
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", conversationwrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(out))
		actual.Failed = err != nil || out.Status.Status == "error" || len(out.Violations) > 0
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = out.Status.Message
		}
		actual.Output, _ = json.Marshal(out.Data)
	}
	input := &conversationread.ConversationInput{Id: "c1", IncludeTranscript: true, Has: &conversationread.ConversationInputHas{Id: true, IncludeTranscript: true, IncludeModelCal: true, IncludeToolCall: true}}
	for _, field := range []struct {
		name    string
		value   any
		present *bool
	}{
		{"id", &input.Id, &input.Has.Id}, {"since", &input.Since, &input.Has.Since},
		{"includeTranscript", &input.IncludeTranscript, &input.Has.IncludeTranscript}, {"includeModelCall", &input.IncludeModelCal, &input.Has.IncludeModelCal}, {"includeToolCall", &input.IncludeToolCall, &input.Has.IncludeToolCall},
	} {
		if raw, ok := in.Filters[field.name]; ok {
			must(json.Unmarshal(raw, field.value))
			*field.present = true
		}
	}
	out := &conversationread.ConversationOutput{}
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", conversationread.ConversationPathURI)), datly.WithInput(input), datly.WithOutput(out))
	must(err)
	for _, row := range out.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runConversationList(ctx context.Context, dao *datly.Service, in request) result {
	must(conversationlist.DefineConversationRowsComponent(ctx, dao))
	input := &conversationlist.ConversationRowsInput{Has: &conversationlist.ConversationRowsInputHas{}}
	value, markers := reflect.ValueOf(input).Elem(), reflect.ValueOf(input.Has).Elem()
	fields := map[string]string{"agentId": "AgentId", "parentId": "ParentId", "parentTurnId": "ParentTurnId", "excludeChildren": "ExcludeChildren", "excludeScheduled": "ExcludeScheduled", "scheduleId": "ScheduleId", "scheduleRunId": "ScheduleRunId", "q": "Query", "status": "StatusFilter", "createdSince": "CreatedSince", "createdBefore": "CreatedBefore", "cursorBefore": "CursorBefore", "cursorAfter": "CursorAfter"}
	for name, field := range fields {
		if raw, ok := in.Filters[name]; ok {
			must(json.Unmarshal(raw, value.FieldByName(field).Addr().Interface()))
			markers.FieldByName(field).SetBool(true)
		}
	}
	out := &conversationlist.ConversationRowsOutput{}
	_, err := dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", conversationlist.ConversationRowsPathURI)), datly.WithInput(input), datly.WithOutput(out))
	must(err)
	actual := result{Rows: []json.RawMessage{}}
	for _, row := range out.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

// runPayload invokes legacy contracts only; fixture setup remains outside the probe.
func runPayload(ctx context.Context, dao *datly.Service, in request) result {
	must(payloadread.DefineComponent(ctx, dao))
	must(payload.DefinePayloadRowsComponent(ctx, dao))
	_, err := payloadwrite.DefineComponent(ctx, dao)
	must(err)
	actual := result{Rows: []json.RawMessage{}}
	if in.Body != "" {
		output := &payloadwrite.Output{}
		req := httptest.NewRequest("PATCH", payloadwrite.PathURI, strings.NewReader(in.Body))
		req.Header.Set("Content-Type", "application/json")
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", payloadwrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
		actual.Failed = err != nil || output.Status.Status == "error" || len(output.Violations) > 0
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = output.Status.Message
		}
		actual.Output, _ = json.Marshal(output.Data)
	}
	binary := &payloadread.Input{Has: &payloadread.Has{}}
	if value, ok := in.Filters["tenantID"]; ok {
		must(json.Unmarshal(value, &binary.TenantID))
		binary.Has.TenantID = true
	}
	if value, ok := in.Filters["ids"]; ok {
		must(json.Unmarshal(value, &binary.Ids))
		binary.Has.Ids = true
	}
	if value, ok := in.Filters["kind"]; ok {
		must(json.Unmarshal(value, &binary.Kind))
		binary.Has.Kind = true
	}
	if value, ok := in.Filters["storage"]; ok {
		must(json.Unmarshal(value, &binary.Storage))
		binary.Has.Storage = true
	}
	if in.Raw {
		input := &payload.PayloadRowsInput{TenantID: binary.TenantID, Ids: binary.Ids, Kind: binary.Kind, Storage: binary.Storage, Has: &payload.PayloadRowsInputHas{TenantID: binary.Has.TenantID, Ids: binary.Has.Ids, Kind: binary.Has.Kind, Storage: binary.Has.Storage}}
		output := &payload.PayloadRowsOutput{}
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", payload.PayloadRowsPathURI)), datly.WithInput(input), datly.WithOutput(output), datly.WithSessionOptions(datly.WithQuerySelectors(in.Selectors...)))
		must(err)
		for _, row := range output.Data {
			raw, err := json.Marshal(row)
			must(err)
			actual.Rows = append(actual.Rows, raw)
		}
	} else {
		output := &payloadread.Output{}
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", payloadread.PayloadURI)), datly.WithInput(binary), datly.WithOutput(output), datly.WithSessionOptions(datly.WithQuerySelectors(in.Selectors...)))
		must(err)
		for _, row := range output.Data {
			raw, err := json.Marshal(row)
			must(err)
			actual.Rows = append(actual.Rows, raw)
		}
	}
	return actual
}

func runGeneratedFile(ctx context.Context, dao *datly.Service, in request) result {
	must(fileread.DefineComponent(ctx, dao))
	_, err := filewrite.DefineComponent(ctx, dao)
	must(err)
	actual := result{Rows: []json.RawMessage{}}
	if in.Body != "" {
		output := &filewrite.Output{}
		req := httptest.NewRequest("PATCH", filewrite.PathURI, strings.NewReader(in.Body))
		req.Header.Set("Content-Type", "application/json")
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", filewrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
		actual.Failed = err != nil || output.Status.Status == "error" || len(output.Violations) > 0
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = output.Status.Message
		}
		actual.Output, _ = json.Marshal(output.Data)
	}
	input := &fileread.Input{Has: &fileread.Has{}}
	// Reuse the real legacy tags for parameter values and their presence bits.
	fields := map[string]interface{}{"conversationId": &input.ConversationID, "turnId": &input.TurnID, "messageId": &input.MessageID, "id": &input.ID, "provider": &input.Provider, "status": &input.Status, "since": &input.Since}
	for name, destination := range fields {
		if value, present := in.Filters[name]; present {
			must(json.Unmarshal(value, destination))
			switch name {
			case "conversationId":
				input.Has.ConversationID = true
			case "turnId":
				input.Has.TurnID = true
			case "messageId":
				input.Has.MessageID = true
			case "id":
				input.Has.ID = true
			case "provider":
				input.Has.Provider = true
			case "status":
				input.Has.Status = true
			case "since":
				input.Has.Since = true
			}
		}
	}
	output := &fileread.Output{}
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", fileread.URI)), datly.WithInput(input), datly.WithOutput(output))
	must(err)
	for _, row := range output.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runUser(ctx context.Context, dao *datly.Service, in request) result {
	must(userread.DefineUserComponent(ctx, dao))
	_, err := userwrite.DefineComponent(ctx, dao)
	must(err)
	actual := result{Rows: []json.RawMessage{}}
	if in.Body != "" {
		output := &userwrite.Output{}
		req := httptest.NewRequest("PATCH", userwrite.PathURI, strings.NewReader(in.Body))
		req.Header.Set("Content-Type", "application/json")
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", userwrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
		actual.Failed = err != nil || output.Status.Status == "error" || len(output.Violations) > 0
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = output.Status.Message
		}
		actual.Output, _ = json.Marshal(output.Data)
	}
	input := &userread.UserInput{Has: &userread.UserInputHas{}}
	if value, ok := in.Filters["id"]; ok {
		must(json.Unmarshal(value, &input.Id))
		input.Has.Id = true
	}
	if value, ok := in.Filters["username"]; ok {
		must(json.Unmarshal(value, &input.Username))
		input.Has.Username = true
	}
	output := &userread.UserOutput{}
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", userread.UserPathURI)), datly.WithInput(input), datly.WithOutput(output))
	must(err)
	for _, row := range output.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runSession(ctx context.Context, dao *datly.Service, in request) result {
	must(sessionread.DefineSessionComponent(ctx, dao))
	_, err := sessionwrite.DefineComponent(ctx, dao)
	must(err)
	_, err = sessiondelete.DefineComponent(ctx, dao)
	must(err)
	actual := result{Rows: []json.RawMessage{}}
	if in.Component == "sessionDelete" {
		var ids []string
		if value, ok := in.Filters["ids"]; ok {
			must(json.Unmarshal(value, &ids))
		}
		output := &sessiondelete.Output{}
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", sessiondelete.PathURI)), datly.WithInput(&sessiondelete.Input{Ids: ids}), datly.WithOutput(output))
		actual.Failed = err != nil || output.Status.Status == "error" || len(output.Violations) > 0
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = output.Status.Message
		}
		actual.Output, _ = json.Marshal(output.Data)
	} else if in.Body != "" {
		output := &sessionwrite.Output{}
		req := httptest.NewRequest("PATCH", sessionwrite.PathURI, strings.NewReader(in.Body))
		req.Header.Set("Content-Type", "application/json")
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", sessionwrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
		actual.Failed = err != nil || output.Status.Status == "error" || len(output.Violations) > 0
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = output.Status.Message
		}
		actual.Output, _ = json.Marshal(output.Data)
	}
	input := &sessionread.SessionInput{Has: &sessionread.SessionInputHas{}}
	if value, ok := in.Filters["id"]; ok {
		must(json.Unmarshal(value, &input.Id))
		input.Has.Id = true
	}
	output := &sessionread.SessionOutput{}
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", sessionread.SessionPathURI)), datly.WithInput(input), datly.WithOutput(output))
	must(err)
	for _, row := range output.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runOAuthToken(ctx context.Context, dao *datly.Service, in request) result {
	must(oauthread.DefineTokenComponent(ctx, dao))
	_, err := oauthwrite.DefineComponent(ctx, dao)
	must(err)
	actual := result{Rows: []json.RawMessage{}}
	if in.Body != "" {
		output := &oauthwrite.Output{}
		req := httptest.NewRequest("PATCH", oauthwrite.PathURI, strings.NewReader(in.Body))
		req.Header.Set("Content-Type", "application/json")
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", oauthwrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
		actual.Failed = err != nil || output.Status.Status == "error" || len(output.Violations) > 0
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = output.Status.Message
		}
		actual.Output, _ = json.Marshal(output.Data)
	}
	input := &oauthread.TokenInput{Has: &oauthread.TokenInputHas{}}
	if value, ok := in.Filters["userId"]; ok {
		must(json.Unmarshal(value, &input.Id))
		input.Has.Id = true
	}
	output := &oauthread.TokenOutput{}
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", oauthread.TokenPathURI)), datly.WithInput(input), datly.WithOutput(output))
	must(err)
	for _, row := range output.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runLinkStateWrite(ctx context.Context, dao *datly.Service, in request) result {
	must(linkread.DefineLinkStateComponent(ctx, dao))
	_, err := linkwrite.DefineComponent(ctx, dao)
	must(err)
	actual := result{Rows: []json.RawMessage{}}
	if in.Body != "" {
		out := &linkwrite.Output{}
		req := httptest.NewRequest("PATCH", linkwrite.PathURI, strings.NewReader(in.Body))
		req.Header.Set("Content-Type", "application/json")
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", linkwrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(out))
		actual.Failed = err != nil || out.Status.Status == "error"
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = out.Status.Message
		}
		actual.Output, _ = json.Marshal(struct {
			Data    *linkwrite.LinkState
			Created bool
		}{out.Data, out.Created})
	}
	input := &linkread.LinkStateInput{Has: &linkread.LinkStateInputHas{}}
	if value, ok := in.Filters["flowHash"]; ok {
		must(json.Unmarshal(value, &input.FlowHash))
		input.Has.FlowHash = true
	}
	out := &linkread.LinkStateOutput{}
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", linkread.LinkStatePathURI)), datly.WithInput(input), datly.WithOutput(out))
	must(err)
	for _, row := range out.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runSchedule(ctx context.Context, dao *datly.Service, in request) result {
	must(scheduleread.DefineScheduleComponent(ctx, dao))
	must(scheduleread.DefineScheduleListComponent(ctx, dao))
	must(scheduleread.DefineScheduleRunDueListComponent(ctx, dao))
	actual := result{Rows: []json.RawMessage{}}
	if in.Component == "scheduleDelete" {
		_, err := scheduledelete.DefineComponent(ctx, dao)
		must(err)
		var ids []string
		if raw, ok := in.Filters["ids"]; ok {
			must(json.Unmarshal(raw, &ids))
		}
		output := &scheduledelete.Output{}
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", scheduledelete.PathURI)), datly.WithInput(&scheduledelete.Input{Ids: ids}), datly.WithOutput(output))
		actual.Failed = err != nil || output.Status.Status == "error" || len(output.Violations) > 0
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = output.Status.Message
		}
		actual.Output, _ = json.Marshal(output.Data)
	} else if in.Body != "" {
		_, err := schedulewrite.DefineComponent(ctx, dao)
		must(err)
		output := &schedulewrite.Output{}
		req := httptest.NewRequest("PATCH", schedulewrite.PathURI, strings.NewReader(in.Body))
		req.Header.Set("Content-Type", "application/json")
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", schedulewrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
		actual.Failed = err != nil || output.Status.Status == "error" || len(output.Violations) > 0
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = output.Status.Message
		}
		actual.Output, _ = json.Marshal(output.Data)
	}
	var mode string
	if raw, ok := in.Filters["mode"]; ok {
		must(json.Unmarshal(raw, &mode))
	}
	var input any
	path := scheduleread.SchedulePathURI
	switch mode {
	case "due":
		input = &scheduleread.ScheduleRunDueListInput{}
		path = scheduleread.SchedulePathListRunDueURI
	case "list":
		input = &scheduleread.ScheduleListInput{}
		path = scheduleread.SchedulePathListURI
	default:
		typed := &scheduleread.ScheduleInput{Has: &scheduleread.ScheduleInputHas{}}
		if raw, ok := in.Filters["id"]; ok {
			must(json.Unmarshal(raw, &typed.Id))
			typed.Has.Id = true
		}
		input = typed
	}
	output := &scheduleread.ScheduleOutput{}
	_, err := dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", path)), datly.WithInput(input), datly.WithOutput(output))
	if err != nil {
		actual.Failed = true
		actual.Error = err.Error()
		return actual
	}
	for _, row := range output.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runScheduleLease(ctx context.Context, dao *datly.Service, in request) result {
	var action string
	if raw, ok := in.Filters["action"]; ok {
		must(json.Unmarshal(raw, &action))
	}
	var failed bool
	var message string
	var encoded json.RawMessage
	if action == "release" {
		_, err := schedulelease.DefineReleaseLeaseComponent(ctx, dao)
		must(err)
		input := &schedulelease.ReleaseLeaseInput{}
		must(json.Unmarshal([]byte(in.Body), input))
		output := &schedulelease.ReleaseLeaseOutput{}
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("POST", schedulelease.ReleaseLeasePathURI)), datly.WithInput(input), datly.WithOutput(output))
		failed = err != nil || output.Status.Status == "error"
		if err != nil {
			message = err.Error()
		} else {
			message = output.Status.Message
		}
		encoded, _ = json.Marshal(output.Released)
	} else {
		_, err := schedulelease.DefineClaimLeaseComponent(ctx, dao)
		must(err)
		input := &schedulelease.ClaimLeaseInput{}
		must(json.Unmarshal([]byte(in.Body), input))
		output := &schedulelease.ClaimLeaseOutput{}
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("POST", schedulelease.ClaimLeasePathURI)), datly.WithInput(input), datly.WithOutput(output))
		failed = err != nil || output.Status.Status == "error"
		if err != nil {
			message = err.Error()
		} else {
			message = output.Status.Message
		}
		encoded, _ = json.Marshal(output.Claimed)
	}
	in.Body = ""
	in.Component = "schedule"
	in.Filters = map[string]json.RawMessage{"mode": json.RawMessage(`"due"`)}
	actual := runSchedule(ctx, dao, in)
	actual.Failed = actual.Failed || failed
	if failed {
		actual.Error = message
	}
	actual.Output = encoded
	return actual
}

func runRun(ctx context.Context, dao *datly.Service, in request) result {
	must(runread.DefineRunRowsComponent(ctx, dao))
	_, err := runwrite.DefineComponent(ctx, dao)
	must(err)
	actual := result{Rows: []json.RawMessage{}}
	if in.Body != "" {
		output := &runwrite.Output{}
		req := httptest.NewRequest("PATCH", runwrite.PathURI, strings.NewReader(in.Body))
		req.Header.Set("Content-Type", "application/json")
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", runwrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
		actual.Failed = err != nil || output.Status.Status == "error" || len(output.Violations) > 0
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = output.Status.Message
		}
		actual.Output, _ = json.Marshal(output.Data)
	}
	input := &runread.RunRowsInput{Has: &runread.RunRowsInputHas{}}
	for _, field := range []struct {
		name    string
		value   *string
		present *bool
	}{{"id", &input.Id, &input.Has.Id}, {"turnId", &input.TurnId, &input.Has.TurnId}, {"conversationId", &input.ConversationId, &input.Has.ConversationId}, {"scheduleId", &input.ScheduleId, &input.Has.ScheduleId}, {"workerId", &input.WorkerId, &input.Has.WorkerId}, {"status", &input.RunStatus, &input.Has.RunStatus}} {
		if raw, ok := in.Filters[field.name]; ok {
			must(json.Unmarshal(raw, field.value))
			*field.present = true
		}
	}
	if raw, ok := in.Filters["excludeStatuses"]; ok {
		must(json.Unmarshal(raw, &input.ExcludeStatuses))
		input.Has.ExcludeStatuses = true
	}
	if in.Raw {
		input.DefaultPredicate = "1"
		input.Has.DefaultPredicate = true
	}
	ctx = context.WithValue(ctx, reflect.TypeOf(input), input)
	output := &runread.RunRowsOutput{}
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", runread.RunRowsPathURI)), datly.WithInput(input), datly.WithOutput(output))
	must(err)
	if output.Status.Status == "error" {
		actual.Failed = true
		actual.Error = "legacy read: " + output.Status.Message
	}
	for _, row := range output.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runRunVariant(ctx context.Context, dao *datly.Service, in request) result {
	actual := result{Rows: []json.RawMessage{}}
	if in.Component == "runActive" {
		must(runactive.DefineActiveRunsComponent(ctx, dao))
		input := &runactive.ActiveRunsInput{Has: &runactive.ActiveRunsInputHas{}}
		if raw, ok := in.Filters["conversationId"]; ok {
			must(json.Unmarshal(raw, &input.ConversationId))
			input.Has.ConversationId = true
		}
		if raw, ok := in.Filters["turnId"]; ok {
			must(json.Unmarshal(raw, &input.TurnId))
			input.Has.TurnId = true
		}
		output := &runactive.ActiveRunsOutput{}
		_, err := dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", runactive.ActiveRunsPathURI)), datly.WithInput(input), datly.WithOutput(output))
		must(err)
		actual.Failed = output.Status.Status == "error"
		actual.Error = output.Status.Message
		for _, row := range output.Data {
			raw, err := json.Marshal(row)
			must(err)
			actual.Rows = append(actual.Rows, raw)
		}
	} else {
		must(runstale.DefineStaleRunsComponent(ctx, dao))
		input := &runstale.StaleRunsInput{Has: &runstale.StaleRunsInputHas{}}
		for name, raw := range in.Filters {
			value := reflect.ValueOf(input).Elem()
			has := reflect.ValueOf(input.Has).Elem()
			fieldName := map[string]string{"heartbeatBefore": "HeartbeatBefore", "workerHost": "WorkerHost", "leaseExpiredBefore": "LeaseExpiredBefore", "activityAfter": "ActivityAfter", "conversationKind": "ConversationKind", "rootInteractive": "RootInteractive"}[name]
			if fieldName != "" {
				must(json.Unmarshal(raw, value.FieldByName(fieldName).Addr().Interface()))
				has.FieldByName(fieldName).SetBool(true)
			}
		}
		output := &runstale.StaleRunsOutput{}
		_, err := dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", runstale.StaleRunsPathURI)), datly.WithInput(input), datly.WithOutput(output))
		must(err)
		actual.Failed = output.Status.Status == "error"
		actual.Error = output.Status.Message
		for _, row := range output.Data {
			raw, err := json.Marshal(row)
			must(err)
			actual.Rows = append(actual.Rows, raw)
		}
	}
	return actual
}

func runRunLease(ctx context.Context, dao *datly.Service, in request) result {
	var action string
	if raw, ok := in.Filters["action"]; ok {
		must(json.Unmarshal(raw, &action))
	}
	var failed bool
	var message string
	var encoded json.RawMessage
	if action == "release" {
		_, err := runlease.DefineReleaseLeaseComponent(ctx, dao)
		must(err)
		input := &runlease.ReleaseLeaseInput{}
		must(json.Unmarshal([]byte(in.Body), input))
		output := &runlease.ReleaseLeaseOutput{}
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("POST", runlease.ReleaseLeasePathURI)), datly.WithInput(input), datly.WithOutput(output))
		failed = err != nil || output.Status.Status == "error"
		if err != nil {
			message = err.Error()
		} else {
			message = output.Status.Message
		}
		encoded, _ = json.Marshal(output.Released)
	} else {
		_, err := runlease.DefineClaimLeaseComponent(ctx, dao)
		must(err)
		input := &runlease.ClaimLeaseInput{}
		must(json.Unmarshal([]byte(in.Body), input))
		output := &runlease.ClaimLeaseOutput{}
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("POST", runlease.ClaimLeasePathURI)), datly.WithInput(input), datly.WithOutput(output))
		failed = err != nil || output.Status.Status == "error"
		if err != nil {
			message = err.Error()
		} else {
			message = output.Status.Message
		}
		encoded, _ = json.Marshal(output.Claimed)
	}
	in.Body = ""
	in.Component = "run"
	in.Raw = true
	in.Filters = nil
	actual := runRun(ctx, dao, in)
	actual.Failed = actual.Failed || failed
	if failed {
		actual.Error = message
	}
	actual.Output = encoded
	return actual
}

func runSchedulerRunList(ctx context.Context, dao *datly.Service, in request) result {
	must(schedrun.DefineRunListComponent(ctx, dao))
	input := &schedrun.RunListInput{EffectiveUserID: in.Principal, Has: &schedrun.RunListInputHas{EffectiveUserID: true}}
	for name, raw := range in.Filters {
		fieldName := map[string]string{"limit": "Limit", "offset": "Offset", "scheduleId": "ScheduleId", "status": "RunStatus", "conversationId": "ConversationId", "errorMessage": "ErrorMessage"}[name]
		if fieldName != "" {
			value := reflect.ValueOf(input).Elem().FieldByName(fieldName)
			must(json.Unmarshal(raw, value.Addr().Interface()))
			reflect.ValueOf(input.Has).Elem().FieldByName(fieldName).SetBool(true)
		}
	}
	if !input.Has.Limit {
		input.Limit = 100
		input.Has.Limit = true
	}
	output := &schedrun.RunListOutput{}
	_, err := dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", schedrun.RunListPathURI)), datly.WithInput(input), datly.WithOutput(output))
	must(err)
	actual := result{Rows: []json.RawMessage{}, Failed: output.Status.Status == "error", Error: output.Status.Message}
	for _, row := range output.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runSchedulerRuns(ctx context.Context, dao *datly.Service, in request) result {
	must(schedrun.DefineRunComponent(ctx, dao))
	must(schedrun.DefineRunDueComponent(ctx, dao))
	public := &schedrun.RunInput{EffectiveUserID: in.Principal, Has: &schedrun.RunInputHas{EffectiveUserID: in.Principal != ""}}
	internal := &schedrun.RunDueInput{Has: &schedrun.RunDueInputHas{}}
	for name, raw := range in.Filters {
		fieldName := map[string]string{"scheduleId": "Id", "since": "Since", "scheduledFor": "ScheduledFor", "excludeStatuses": "ExcludeStatuses"}[name]
		if fieldName != "" {
			for _, input := range []any{public, internal} {
				value := reflect.ValueOf(input).Elem()
				must(json.Unmarshal(raw, value.FieldByName(fieldName).Addr().Interface()))
				value.FieldByName("Has").Elem().FieldByName(fieldName).SetBool(true)
			}
		}
	}
	var input any = public
	path := schedrun.RunPathURI
	if in.Component == "schedulerRunDue" {
		input = internal
		path = schedrun.RunPathRunDueURI
	}
	output := &schedrun.RunOutput{}
	_, err := dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", path)), datly.WithInput(input), datly.WithOutput(output))
	must(err)
	actual := result{Rows: []json.RawMessage{}, Failed: output.Status.Status == "error", Error: output.Status.Message}
	for _, row := range output.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runSchedulerRunTotal(ctx context.Context, dao *datly.Service, in request) result {
	must(schedrun.DefineRunTotalComponent(ctx, dao))
	input := &schedrun.RunTotalInput{EffectiveUserID: in.Principal, Has: &schedrun.RunTotalInputHas{EffectiveUserID: true}}
	for name, raw := range in.Filters {
		fieldName := map[string]string{"scheduleId": "ScheduleId", "status": "RunStatus", "conversationId": "ConversationId", "errorMessage": "ErrorMessage"}[name]
		if fieldName != "" {
			must(json.Unmarshal(raw, reflect.ValueOf(input).Elem().FieldByName(fieldName).Addr().Interface()))
			reflect.ValueOf(input.Has).Elem().FieldByName(fieldName).SetBool(true)
		}
	}
	output := &schedrun.RunTotalOutput{}
	_, err := dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", schedrun.RunTotalPathURI)), datly.WithInput(input), datly.WithOutput(output))
	must(err)
	actual := result{Rows: []json.RawMessage{}, Failed: output.Status.Status == "error", Error: output.Status.Message}
	for _, row := range output.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

// Isolated fixture probe only: bind the original contracts and preserve their
// declared presence markers instead of recreating legacy query behavior.
func runTurnReader(ctx context.Context, dao *datly.Service, in request) result {
	var mode string
	if raw, ok := in.Filters["mode"]; ok {
		must(json.Unmarshal(raw, &mode))
	}
	var input, output any
	var path string
	switch mode {
	case "queuedCount":
		must(turncount.DefineQueuedTotalComponent(ctx, dao))
		input = &turncount.QueuedTotalInput{}
		output = &turncount.QueuedTotalOutput{}
		path = turncount.QueuedTotalPathURI
	case "controllerCount":
		must(turncontroller.DefineControllerTotalComponent(ctx, dao))
		input = &turncontroller.ControllerTotalInput{}
		output = &turncontroller.ControllerTotalOutput{}
		path = turncontroller.ControllerTotalPathURI
	case "active":
		must(turnactive.DefineActiveTurnsComponent(ctx, dao))
		input = &turnactive.ActiveTurnsInput{}
		output = &turnactive.ActiveTurnsOutput{}
		path = turnactive.ActiveTurnsPathURI
	case "byId":
		must(turnlookup.DefineTurnLookupComponent(ctx, dao))
		input = &turnlookup.TurnLookupInput{}
		output = &turnlookup.TurnLookupOutput{}
		path = turnlookup.TurnLookupPathURI
	case "nextQueued":
		must(turnnext.DefineQueuedTurnComponent(ctx, dao))
		input = &turnnext.QueuedTurnInput{}
		output = &turnnext.QueuedTurnOutput{}
		path = turnnext.QueuedTurnPathURI
	case "queued":
		must(turnqueued.DefineQueuedTurnsComponent(ctx, dao))
		input = &turnqueued.QueuedTurnsInput{}
		output = &turnqueued.QueuedTurnsOutput{}
		path = turnqueued.QueuedTurnsPathURI
	default:
		must(turnlist.DefineTurnRowsComponent(ctx, dao))
		input = &turnlist.TurnRowsInput{}
		output = &turnlist.TurnRowsOutput{}
		path = turnlist.TurnRowsPathURI
	}
	value := reflect.ValueOf(input).Elem()
	has := value.FieldByName("Has")
	has.Set(reflect.New(has.Type().Elem()))
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		tag := field.Tag.Get("parameter")
		for _, part := range strings.Split(tag, ",") {
			if !strings.HasPrefix(part, "in=") {
				continue
			}
			name := strings.TrimPrefix(part, "in=")
			raw, ok := in.Filters[name]
			if in.Component == "messageReader" && !ok {
				if name == "convId" {
					raw, ok = in.Filters["conversationId"]
				}
				if name == "elicId" {
					raw, ok = in.Filters["elicitationId"]
				}
			}
			if !ok {
				continue
			}
			must(json.Unmarshal(raw, value.Field(i).Addr().Interface()))
			has.Elem().FieldByName(field.Name).SetBool(true)
		}
	}
	_, err := dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", path)), datly.WithInput(input), datly.WithOutput(output))
	actual := result{Failed: err != nil, Rows: []json.RawMessage{}}
	if err != nil {
		actual.Error = err.Error()
		return actual
	}
	rows := reflect.ValueOf(output).Elem().FieldByName("Data")
	for i := 0; i < rows.Len(); i++ {
		raw, err := json.Marshal(rows.Index(i).Interface())
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runModelCall(ctx context.Context, dao *datly.Service, in request) result {
	if in.Method == "DELETE" {
		_, err := modelcallwrite.DefineDeleteComponent(ctx, dao)
		must(err)
		output := &modelcallwrite.DeleteOutput{}
		req := httptest.NewRequest("DELETE", modelcallwrite.PathURI, strings.NewReader(in.Body))
		req.Header.Set("Content-Type", "application/json")
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", modelcallwrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
		actual := result{Failed: err != nil || output.Status.Status == "error", Rows: []json.RawMessage{}}
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = output.Status.Message
		}
		return actual
	}
	_, err := modelcallwrite.DefineComponent(ctx, dao)
	must(err)
	output := &modelcallwrite.Output{}
	req := httptest.NewRequest("PATCH", modelcallwrite.PathURI, strings.NewReader(in.Body))
	req.Header.Set("Content-Type", "application/json")
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", modelcallwrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
	actual := result{Failed: err != nil || output.Status.Status == "error" || len(output.Violations) > 0, Rows: []json.RawMessage{}}
	if err != nil {
		actual.Error = err.Error()
	} else {
		actual.Error = output.Status.Message
	}
	actual.Output, _ = json.Marshal(output.Data)
	return actual
}

func runModelCallTranscript(ctx context.Context, dao *datly.Service, in request) result {
	if in.Filters == nil {
		in.Filters = map[string]json.RawMessage{}
	}
	in.Filters["includeModelCall"] = json.RawMessage("true")
	in.Filters["includeTranscript"] = json.RawMessage("true")
	original := runConversation(ctx, dao, in)
	actual := result{Failed: original.Failed, Error: original.Error, Rows: []json.RawMessage{}}
	for _, raw := range original.Rows {
		var conversation conversationread.ConversationView
		must(json.Unmarshal(raw, &conversation))
		for _, turn := range conversation.Transcript {
			if turn == nil {
				continue
			}
			for _, message := range turn.Message {
				if message != nil && message.ModelCall != nil {
					raw, err := json.Marshal(message.ModelCall)
					must(err)
					actual.Rows = append(actual.Rows, raw)
				}
			}
		}
	}
	return actual
}

func runModelCallUsage(ctx context.Context, dao *datly.Service, in request) result {
	var models bool
	if raw, ok := in.Filters["models"]; ok {
		must(json.Unmarshal(raw, &models))
	}
	original := runConversation(ctx, dao, in)
	actual := result{Failed: original.Failed, Error: original.Error, Rows: []json.RawMessage{}}
	for _, raw := range original.Rows {
		var conversation conversationread.ConversationView
		must(json.Unmarshal(raw, &conversation))
		if conversation.Usage == nil {
			continue
		}
		if models {
			for _, model := range conversation.Usage.Model {
				raw, err := json.Marshal(model)
				must(err)
				actual.Rows = append(actual.Rows, raw)
			}
		} else {
			raw, err := json.Marshal(conversation.Usage)
			must(err)
			actual.Rows = append(actual.Rows, raw)
		}
	}
	return actual
}

func runToolCall(ctx context.Context, dao *datly.Service, in request) result {
	if in.Method == "DELETE" {
		_, err := toolcallwrite.DefineDeleteComponent(ctx, dao)
		must(err)
		output := &toolcallwrite.DeleteOutput{}
		req := httptest.NewRequest("DELETE", toolcallwrite.PathURI, strings.NewReader(in.Body))
		req.Header.Set("Content-Type", "application/json")
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", toolcallwrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
		actual := result{Failed: err != nil || output.Status.Status == "error", Rows: []json.RawMessage{}}
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = output.Status.Message
		}
		return actual
	}

	_, err := toolcallwrite.DefineComponent(ctx, dao)
	must(err)
	output := &toolcallwrite.Output{}
	req := httptest.NewRequest("PATCH", toolcallwrite.PathURI, strings.NewReader(in.Body))
	req.Header.Set("Content-Type", "application/json")
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", toolcallwrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
	actual := result{Failed: err != nil || output.Status.Status == "error" || len(output.Violations) > 0, Rows: []json.RawMessage{}}
	if err != nil {
		actual.Error = err.Error()
	} else {
		actual.Error = output.Status.Message
	}
	actual.Output, _ = json.Marshal(output.Data)
	return actual
}

func runToolCallReader(ctx context.Context, dao *datly.Service, in request) result {
	var mode string
	if raw, ok := in.Filters["mode"]; ok {
		must(json.Unmarshal(raw, &mode))
	}
	var input, output any
	var path string
	switch mode {
	case "byTurn":
		must(toolbyturn.DefineToolCallRowsComponent(ctx, dao))
		input = &toolbyturn.ToolCallRowsInput{}
		output = &toolbyturn.ToolCallRowsOutput{}
		path = toolbyturn.ToolCallRowsPathURI
	case "scopedByOp":
		must(toolbyop.DefineToolCallRowsComponent(ctx, dao))
		input = &toolbyop.ToolCallRowsInput{}
		output = &toolbyop.ToolCallRowsOutput{}
		path = toolbyop.ToolCallRowsPathURI
	default:
		_, err := toolread.DefineComponent(ctx, dao)
		must(err)
		input = &toolread.ByOpInput{}
		output = &toolread.ByOpOutput{}
		path = toolread.PathURI
	}

	value := reflect.ValueOf(input).Elem()
	has := value.FieldByName("Has")
	has.Set(reflect.New(has.Type().Elem()))
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		tag := field.Tag.Get("parameter")
		for _, part := range strings.Split(tag, ",") {
			if !strings.HasPrefix(part, "in=") {
				continue
			}
			name := strings.TrimPrefix(part, "in=")
			raw, ok := in.Filters[name]
			if in.Component == "messageReader" && !ok {
				if name == "convId" {
					raw, ok = in.Filters["conversationId"]
				}
				if name == "elicId" {
					raw, ok = in.Filters["elicitationId"]
				}
			}
			if !ok {
				continue
			}
			must(json.Unmarshal(raw, value.Field(i).Addr().Interface()))
			has.Elem().FieldByName(field.Name).SetBool(true)
		}
	}
	_, err := dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", path)), datly.WithInput(input), datly.WithOutput(output))
	actual := result{Failed: err != nil, Rows: []json.RawMessage{}}
	if err != nil {
		actual.Error = err.Error()
		return actual
	}
	rows := reflect.ValueOf(output).Elem().FieldByName("Data")
	for i := 0; i < rows.Len(); i++ {
		raw, err := json.Marshal(rows.Index(i).Interface())
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runMessage(ctx context.Context, dao *datly.Service, in request) result {
	if in.Method == "DELETE" {
		_, err := messagewrite.DefineDeleteComponent(ctx, dao)
		must(err)
		output := &messagewrite.DeleteOutput{}
		req := httptest.NewRequest("DELETE", messagewrite.PathURI, strings.NewReader(in.Body))
		req.Header.Set("Content-Type", "application/json")
		_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", messagewrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
		actual := result{Failed: err != nil || output.Status.Status == "error", Rows: []json.RawMessage{}}
		if err != nil {
			actual.Error = err.Error()
		} else {
			actual.Error = output.Status.Message
		}
		return actual
	}
	_, err := messagewrite.DefineComponent(ctx, dao)
	must(err)
	output := &messagewrite.Output{}
	req := httptest.NewRequest("PATCH", messagewrite.PathURI, strings.NewReader(in.Body))
	req.Header.Set("Content-Type", "application/json")
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", messagewrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
	actual := result{Failed: err != nil || output.Status.Status == "error" || len(output.Violations) > 0, Rows: []json.RawMessage{}}
	if err != nil {
		actual.Error = err.Error()
	} else {
		actual.Error = output.Status.Message
	}
	actual.Output, _ = json.Marshal(output.Data)
	return actual
}

func runMessageReader(ctx context.Context, dao *datly.Service, in request) result {
	var mode string
	must(json.Unmarshal(in.Filters["mode"], &mode))
	var input, output any
	var path string
	switch mode {
	case "pendingCount":
		must(messagecount.DefineElicitationPendingComponent(ctx, dao))
		input = &messagecount.ElicitationPendingInput{}
		output = &messagecount.ElicitationPendingOutput{}
		path = messagecount.ElicitationPendingPathURI
	case "rows":
		must(messagelist.DefineMessageRowsComponent(ctx, dao))
		input = &messagelist.MessageRowsInput{}
		output = &messagelist.MessageRowsOutput{}
		path = messagelist.MessageRowsPathURI
	case "transcript":
		must(messagelookup.DefineMessageComponent(ctx, dao))
		input = &messagelookup.MessageInput{}
		output = &messagelookup.MessageOutput{}
		path = messagelookup.MessagePathURI
	case "byId":
		must(messageget.DefineMessageComponent(ctx, dao))
		input = &messageget.MessageInput{}
		output = &messageget.MessageOutput{}
		path = messageget.MessagePathURI
	case "elicitation":
		must(messageelicitation.DefineMessageComponent(ctx, dao))
		input = &messageelicitation.MessageInput{}
		output = &messageelicitation.MessageOutput{}
		path = messageelicitation.MessagePathURI
	case "linkedElicitation":
		must(messagelookup.DefineMessageByLinkedConversationAndElicitationComponent(ctx, dao))
		input = &messagelookup.MessageByLinkedConversationAndElicitationInput{}
		output = &messagelookup.MessageByLinkedConversationAndElicitationOutput{}
		path = messagelookup.MessageByLinkedConversationAndElicitationPathURI
	case "parentElicitation":
		must(messagelookup.DefineMessageByParentAndElicitationComponent(ctx, dao))
		input = &messagelookup.MessageByParentAndElicitationInput{}
		output = &messagelookup.MessageByParentAndElicitationOutput{}
		path = messagelookup.MessageByParentAndElicitationPathURI
	default:
		panic("unknown message read mode")
	}

	value := reflect.ValueOf(input).Elem()
	has := value.FieldByName("Has")
	has.Set(reflect.New(has.Type().Elem()))
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		tag := field.Tag.Get("parameter")
		for _, part := range strings.Split(tag, ",") {
			if !strings.HasPrefix(part, "in=") {
				continue
			}
			name := strings.TrimPrefix(part, "in=")
			raw, ok := in.Filters[name]
			if in.Component == "messageReader" && !ok {
				if name == "convId" {
					raw, ok = in.Filters["conversationId"]
				}
				if name == "elicId" {
					raw, ok = in.Filters["elicitationId"]
				}
			}
			if !ok {
				continue
			}
			must(json.Unmarshal(raw, value.Field(i).Addr().Interface()))
			has.Elem().FieldByName(field.Name).SetBool(true)
		}
	}
	_, err := dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", path)), datly.WithInput(input), datly.WithOutput(output))
	actual := result{Failed: err != nil, Rows: []json.RawMessage{}}
	if err != nil {
		actual.Error = err.Error()
		return actual
	}
	rows := reflect.ValueOf(output).Elem().FieldByName("Data")
	for i := 0; i < rows.Len(); i++ {
		raw, err := json.Marshal(rows.Index(i).Interface())
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runApproval(ctx context.Context, dao *datly.Service, in request) result {
	_, err := approvalwrite.DefineComponent(ctx, dao)
	must(err)
	output := &approvalwrite.Output{}
	req := httptest.NewRequest("PATCH", approvalwrite.PathURI, strings.NewReader(in.Body))
	req.Header.Set("Content-Type", "application/json")
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", approvalwrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
	actual := result{Failed: err != nil || output.Status.Status == "error" || len(output.Violations) > 0, Rows: []json.RawMessage{}}
	if err != nil {
		actual.Error = err.Error()
	} else {
		actual.Error = output.Status.Message
	}
	actual.Output, _ = json.Marshal(output.Data)
	return actual
}

func runApprovalReader(ctx context.Context, dao *datly.Service, in request) result {
	var mode string
	must(json.Unmarshal(in.Filters["mode"], &mode))
	var input, output any
	var path string
	switch mode {
	case "rows":
		must(approvalread.DefineQueueRowsComponent(ctx, dao))
		input = &approvalread.QueueRowsInput{}
		output = &approvalread.QueueRowsOutput{}
		path = approvalread.QueueRowsPathURI
	case "outcome":
		must(approvaloutcome.DefineOutcomeRowsComponent(ctx, dao))
		input = &approvaloutcome.OutcomeRowsInput{}
		output = &approvaloutcome.OutcomeRowsOutput{}
		path = approvaloutcome.OutcomeRowsPathURI
	case "count":
		must(approvalcount.DefineQueueTotalComponent(ctx, dao))
		input = &approvalcount.QueueTotalInput{}
		output = &approvalcount.QueueTotalOutput{}
		path = approvalcount.QueueTotalPathURI
	case "pendingCount":
		must(approvalpending.DefinePendingTotalComponent(ctx, dao))
		input = &approvalpending.PendingTotalInput{}
		output = &approvalpending.PendingTotalOutput{}
		path = approvalpending.PendingTotalPathURI
	default:
		panic("unknown approval read mode")
	}

	value := reflect.ValueOf(input).Elem()
	has := value.FieldByName("Has")
	has.Set(reflect.New(has.Type().Elem()))
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		tag := field.Tag.Get("parameter")
		for _, part := range strings.Split(tag, ",") {
			if !strings.HasPrefix(part, "in=") {
				continue
			}
			name := strings.TrimPrefix(part, "in=")
			raw, ok := in.Filters[name]
			if in.Component == "messageReader" && !ok {
				if name == "convId" {
					raw, ok = in.Filters["conversationId"]
				}
				if name == "elicId" {
					raw, ok = in.Filters["elicitationId"]
				}
			}
			if !ok {
				continue
			}
			must(json.Unmarshal(raw, value.Field(i).Addr().Interface()))
			has.Elem().FieldByName(field.Name).SetBool(true)
		}
	}
	_, err := dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", path)), datly.WithInput(input), datly.WithOutput(output))
	actual := result{Failed: err != nil, Rows: []json.RawMessage{}}
	if err != nil {
		actual.Error = err.Error()
		return actual
	}
	rows := reflect.ValueOf(output).Elem().FieldByName("Data")
	for i := 0; i < rows.Len(); i++ {
		raw, err := json.Marshal(rows.Index(i).Interface())
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runStepsReader(ctx context.Context, dao *datly.Service, in request) result {
	must(runsteps.DefineRunStepsComponent(ctx, dao))
	input := &runsteps.RunStepsInput{}
	output := &runsteps.RunStepsOutput{}
	path := runsteps.RunStepsPathURI

	value := reflect.ValueOf(input).Elem()
	has := value.FieldByName("Has")
	has.Set(reflect.New(has.Type().Elem()))
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		tag := field.Tag.Get("parameter")
		for _, part := range strings.Split(tag, ",") {
			if !strings.HasPrefix(part, "in=") {
				continue
			}
			name := strings.TrimPrefix(part, "in=")
			raw, ok := in.Filters[name]
			if in.Component == "messageReader" && !ok {
				if name == "convId" {
					raw, ok = in.Filters["conversationId"]
				}
				if name == "elicId" {
					raw, ok = in.Filters["elicitationId"]
				}
			}
			if !ok {
				continue
			}
			must(json.Unmarshal(raw, value.Field(i).Addr().Interface()))
			has.Elem().FieldByName(field.Name).SetBool(true)
		}
	}
	_, err := dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", path)), datly.WithInput(input), datly.WithOutput(output))
	actual := result{Failed: err != nil, Rows: []json.RawMessage{}}
	if err != nil {
		actual.Error = err.Error()
		return actual
	}
	rows := reflect.ValueOf(output).Elem().FieldByName("Data")
	for i := 0; i < rows.Len(); i++ {
		raw, err := json.Marshal(rows.Index(i).Interface())
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runForgeWriter(ctx context.Context, dao *datly.Service, in request) result {
	_, err := forgewrite.DefineComponent(ctx, dao)
	must(err)
	output := &forgewrite.Output{}
	req := httptest.NewRequest("PATCH", forgewrite.PathURI, strings.NewReader(in.Body))
	req.Header.Set("Content-Type", "application/json")
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("PATCH", forgewrite.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
	actual := result{Failed: err != nil || output.Status.Status == "error", Rows: []json.RawMessage{}}
	if err != nil {
		actual.Error = err.Error()
	} else {
		actual.Error = output.Status.Message
	}
	actual.Output, _ = json.Marshal(output.Data)
	return actual
}
func runForgeStore(ctx context.Context, dao *datly.Service, in request) result {
	client, err := reportingsql.New(ctx, dao, "agently", nil, nil)
	actual := result{Rows: []json.RawMessage{}}
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
		return actual
	}
	if in.SeedExisting {
		seedCtx := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u1"})
		err = client.CreateSharedArtifact(seedCtx, &sharedrecord.Record{
			ArtifactID: "existing", ArtifactRef: "report://existing", OwnerID: "u1",
			Kind: "report", Lifecycle: "saved", Version: 2, Title: "old",
			Document: []byte("{}"), CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		})
		if err != nil {
			actual.Failed, actual.Error = true, fmt.Sprintf("seed existing: %v", err)
			return actual
		}
	}
	var artifact sharedrecord.Record
	if in.Body != "" {
		if err := json.Unmarshal([]byte(in.Body), &artifact); err != nil {
			actual.Failed, actual.Error = true, err.Error()
			return actual
		}
	}
	var artifactID string
	if raw, ok := in.Filters["artifactId"]; ok {
		if err := json.Unmarshal(raw, &artifactID); err != nil {
			actual.Failed, actual.Error = true, err.Error()
			return actual
		}
	}
	switch strings.ToLower(in.Method) {
	case "create":
		err = client.CreateSharedArtifact(ctx, &artifact)
	case "update":
		err = client.UpdateSharedArtifact(ctx, &artifact)
	case "delete":
		err = client.DeleteSharedArtifact(ctx, artifactID)
	case "get":
		var row *sharedrecord.Record
		row, err = client.GetSharedArtifact(ctx, artifactID)
		if err == nil {
			actual.Output, _ = json.Marshal(row)
		}
	case "list":
		var rows []*sharedrecord.Record
		rows, err = client.ListSharedArtifacts(ctx)
		if err == nil {
			actual.Output, _ = json.Marshal(rows)
		}
	default:
		err = fmt.Errorf("unsupported forge store operation %q", in.Method)
	}
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
	}
	return actual
}

func runReportContext(ctx context.Context, dao *datly.Service, in request) result {
	client, err := reportingsql.New(ctx, dao, "agently", nil, nil)
	actual := result{Rows: []json.RawMessage{}}
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
		return actual
	}
	store, ok := client.(*reportingsql.Store)
	if !ok {
		actual.Failed, actual.Error = true, "legacy report context store has unexpected type"
		return actual
	}
	var record reportcontext.Record
	if err := json.Unmarshal([]byte(in.Body), &record); err != nil {
		actual.Failed, actual.Error = true, err.Error()
		return actual
	}
	switch strings.ToLower(in.Method) {
	case "put":
		err = store.PutConversationReportContextCAS(ctx, &record, in.ExpectedRevision)
	case "get":
		var loaded *reportcontext.Record
		loaded, err = store.GetConversationReportContext(ctx, record.ConversationID)
		if err == nil {
			actual.Output, _ = json.Marshal(loaded)
		}
	default:
		err = fmt.Errorf("unsupported report context operation %q", in.Method)
	}
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
	}
	return actual
}

func runReportRun(ctx context.Context, dao *datly.Service, in request) result {
	client, err := reportingsql.New(ctx, dao, "agently", nil, nil)
	actual := result{Rows: []json.RawMessage{}}
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
		return actual
	}
	store, ok := client.(*reportingsql.Store)
	if !ok {
		actual.Failed, actual.Error = true, "legacy report run store has unexpected type"
		return actual
	}
	var record reportrun.Record
	if in.Body != "" {
		if err := json.Unmarshal([]byte(in.Body), &record); err != nil {
			actual.Failed, actual.Error = true, err.Error()
			return actual
		}
	}
	switch strings.ToLower(in.Method) {
	case "create":
		err = store.CreateReportRun(ctx, &record)
	case "update":
		err = store.UpdateReportRunCAS(ctx, &record, in.ExpectedRevision)
	case "get":
		var row *reportrun.Record
		row, err = store.GetReportRun(ctx, record.ReportRunID)
		if err == nil {
			actual.Output, _ = json.Marshal(row)
		}
	case "getbyrequest":
		var row *reportrun.Record
		row, err = store.GetReportRunByRequestID(ctx, record.UIRunRequestID)
		if err == nil {
			actual.Output, _ = json.Marshal(row)
		}
	default:
		err = fmt.Errorf("unsupported report run operation %q", in.Method)
	}
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
	}
	return actual
}

func runReportAdoption(ctx context.Context, dao *datly.Service, in request) result {
	client, err := reportingsql.New(ctx, dao, "agently", nil, nil)
	actual := result{Rows: []json.RawMessage{}}
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
		return actual
	}
	store, ok := client.(*reportingsql.Store)
	if !ok {
		actual.Failed, actual.Error = true, "legacy report adoption store has unexpected type"
		return actual
	}
	var body struct {
		Run     *reportrun.Record     `json:"run"`
		Context *reportcontext.Record `json:"context"`
	}
	if err := json.Unmarshal([]byte(in.Body), &body); err != nil {
		actual.Failed, actual.Error = true, err.Error()
		return actual
	}
	err = store.AdoptReportRunAndContextCAS(ctx, body.Run, in.ExpectedRevision, body.Context, in.ExpectedContextRevision)
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
	}
	return actual
}

func runReportJob(ctx context.Context, dao *datly.Service, in request) result {
	client, err := reportingsql.New(ctx, dao, "agently", nil, nil)
	actual := result{Rows: []json.RawMessage{}}
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
		return actual
	}
	store, ok := client.(*reportingsql.Store)
	if !ok {
		actual.Failed, actual.Error = true, "legacy report job store has unexpected type"
		return actual
	}
	var job reportjob.Record
	if in.Body != "" {
		if err := json.Unmarshal([]byte(in.Body), &job); err != nil {
			actual.Failed, actual.Error = true, err.Error()
			return actual
		}
	}
	switch strings.ToLower(in.Method) {
	case "create":
		err = store.CreateJob(ctx, &job)
	case "update":
		err = store.UpdateJob(ctx, &job)
	case "get":
		var loaded *reportjob.Record
		loaded, err = store.GetJob(ctx, job.JobID)
		if err == nil {
			actual.Output, _ = json.Marshal(loaded)
		}
	case "list":
		var rows []*reportjob.Record
		rows, err = store.ListJobs(ctx)
		if err == nil {
			actual.Output, _ = json.Marshal(rows)
		}
	case "claim":
		if job.StartedAt == nil {
			err = fmt.Errorf("claim start is required")
		} else {
			var loaded *reportjob.Record
			loaded, err = store.ClaimJob(ctx, job.JobID, *job.StartedAt)
			if err == nil {
				actual.Output, _ = json.Marshal(loaded)
			}
		}
	case "fail":
		if job.CompletedAt == nil {
			err = fmt.Errorf("failure completion is required")
		} else {
			var loaded *reportjob.Record
			loaded, err = store.FailJob(ctx, job.JobID, job.Error, job.Diagnostics, *job.CompletedAt)
			if err == nil {
				actual.Output, _ = json.Marshal(loaded)
			}
		}
	case "submit":
		var loaded *reportjob.Record
		var replay bool
		loaded, replay, err = store.SubmitJobFromRun(ctx, &job)
		if err == nil {
			actual.Output, _ = json.Marshal(struct {
				Job    *reportjob.Record `json:"job"`
				Replay bool              `json:"replay"`
			}{loaded, replay})
		}
	default:
		err = fmt.Errorf("unsupported report job operation %q", in.Method)
	}
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
	}
	return actual
}

func runReportArtifact(ctx context.Context, dao *datly.Service, in request) result {
	client, err := reportingsql.New(ctx, dao, "agently", nil, nil)
	actual := result{Rows: []json.RawMessage{}}
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
		return actual
	}
	store, ok := client.(*reportingsql.Store)
	if !ok {
		actual.Failed, actual.Error = true, "legacy report artifact store has unexpected type"
		return actual
	}
	var artifact reportartifact.Record
	if in.Body != "" {
		if err := json.Unmarshal([]byte(in.Body), &artifact); err != nil {
			actual.Failed, actual.Error = true, err.Error()
			return actual
		}
	}
	switch strings.ToLower(in.Method) {
	case "put":
		err = store.PutArtifact(ctx, &artifact)
	case "get":
		var loaded *reportartifact.Record
		loaded, err = store.GetArtifact(ctx, artifact.ArtifactID)
		if err == nil {
			actual.Output, _ = json.Marshal(loaded)
		}
	case "list":
		var rows []*reportartifact.Record
		rows, err = store.ListArtifacts(ctx)
		if err == nil {
			actual.Output, _ = json.Marshal(rows)
		}
	default:
		err = fmt.Errorf("unsupported report artifact operation %q", in.Method)
	}
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
	}
	return actual
}

func runReportComplete(ctx context.Context, dao *datly.Service, in request) result {
	client, err := reportingsql.New(ctx, dao, "agently", nil, nil)
	actual := result{Rows: []json.RawMessage{}}
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
		return actual
	}
	store, ok := client.(*reportingsql.Store)
	if !ok {
		actual.Failed, actual.Error = true, "legacy completion store has unexpected type"
		return actual
	}
	var body struct {
		JobID        string                 `json:"jobId"`
		Artifact     *reportartifact.Record `json:"artifact"`
		Diagnostics  []byte                 `json:"diagnostics"`
		CompletedAt  time.Time              `json:"completedAt"`
		RetentionTTL time.Duration          `json:"retentionTtl"`
	}
	if err := json.Unmarshal([]byte(in.Body), &body); err != nil {
		actual.Failed, actual.Error = true, err.Error()
		return actual
	}
	job, err := store.CompleteJobWithArtifact(ctx, body.JobID, body.Artifact, body.Diagnostics, body.CompletedAt, body.RetentionTTL)
	if err != nil {
		actual.Failed, actual.Error = true, err.Error()
		return actual
	}
	actual.Output, _ = json.Marshal(job)
	return actual
}
func runForgeReader(ctx context.Context, dao *datly.Service, in request) result {
	var mode string
	must(json.Unmarshal(in.Filters["mode"], &mode))
	var input, output any
	var path string
	switch mode {
	case "byId":
		must(forgeread.DefineSharedArtifactComponent(ctx, dao, "agently"))
		input = &forgeread.SharedArtifactInput{}
		output = &forgeread.SharedArtifactOutput{}
		path = forgeread.SharedArtifactPathURI
	case "rows":
		must(forgelist.DefineComponent(ctx, dao, "agently"))
		input = &forgelist.Input{}
		output = &forgelist.Output{}
		path = forgelist.PathURI
	default:
		panic("unknown forge reader mode")
	}
	value := reflect.ValueOf(input).Elem()
	has := value.FieldByName("Has")
	has.Set(reflect.New(has.Type().Elem()))
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		for _, part := range strings.Split(field.Tag.Get("parameter"), ",") {
			if !strings.HasPrefix(part, "in=") {
				continue
			}
			raw, ok := in.Filters[strings.TrimPrefix(part, "in=")]
			if !ok {
				continue
			}
			must(json.Unmarshal(raw, value.Field(i).Addr().Interface()))
			has.Elem().FieldByName(field.Name).SetBool(true)
		}
	}
	_, err := dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", path)), datly.WithInput(input), datly.WithOutput(output))
	actual := result{Failed: err != nil, Rows: []json.RawMessage{}}
	if err != nil {
		actual.Error = err.Error()
		return actual
	}
	rows := reflect.ValueOf(output).Elem().FieldByName("Data")
	for i := 0; i < rows.Len(); i++ {
		raw, err := json.Marshal(rows.Index(i).Interface())
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runLinkStateReader(ctx context.Context, dao *datly.Service, in request) result {
	must(linkread.DefineLinkStateComponent(ctx, dao))
	input := &linkread.LinkStateInput{Has: &linkread.LinkStateInputHas{}}
	if raw, ok := in.Filters["flowHash"]; ok {
		must(json.Unmarshal(raw, &input.FlowHash))
		input.Has.FlowHash = true
	}
	output := &linkread.LinkStateOutput{}
	_, err := dao.Operate(ctx, datly.WithPath(contract.NewPath("GET", linkread.LinkStatePathURI)), datly.WithInput(input), datly.WithOutput(output))
	actual := result{Failed: err != nil, Rows: []json.RawMessage{}}
	if err != nil {
		actual.Error = err.Error()
		return actual
	}
	for _, row := range output.Data {
		raw, err := json.Marshal(row)
		must(err)
		actual.Rows = append(actual.Rows, raw)
	}
	return actual
}

func runLinkStateConsume(ctx context.Context, dao *datly.Service, in request) result {
	_, err := linkconsume.DefineComponent(ctx, dao)
	must(err)
	output := &linkconsume.Output{}
	req := httptest.NewRequest("POST", linkconsume.PathURI, strings.NewReader(in.Body))
	req.Header.Set("Content-Type", "application/json")
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("POST", linkconsume.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
	actual := result{Failed: err != nil || output.Status.Status == "error", Rows: []json.RawMessage{}}
	if err != nil {
		actual.Error = err.Error()
	} else {
		actual.Error = output.Status.Message
	}
	actual.Output, _ = json.Marshal(map[string]any{"outcome": output.Outcome})
	return actual
}

func runLinkStateCleanup(ctx context.Context, dao *datly.Service, in request) result {
	_, err := linkcleanup.DefineComponent(ctx, dao)
	must(err)
	output := &linkcleanup.Output{}
	req := httptest.NewRequest("DELETE", linkcleanup.PathURI, strings.NewReader(in.Body))
	req.Header.Set("Content-Type", "application/json")
	_, err = dao.Operate(ctx, datly.WithPath(contract.NewPath("DELETE", linkcleanup.PathURI)), datly.WithSessionOptions(datly.WithRequest(req)), datly.WithOutput(output))
	actual := result{Failed: err != nil || output.Status.Status == "error", Rows: []json.RawMessage{}}
	if err != nil {
		actual.Error = err.Error()
	} else {
		actual.Error = output.Status.Message
	}
	actual.Output, _ = json.Marshal(map[string]any{"deleted": output.Deleted, "oldestExpiresAt": output.OldestExpiresAt})
	return actual
}
