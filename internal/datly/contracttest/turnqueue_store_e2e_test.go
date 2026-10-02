package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	read "github.com/viant/agently-core/internal/datly/turnqueue/read"
	write "github.com/viant/agently-core/internal/datly/turnqueue/write"
	queue "github.com/viant/agently-core/internal/store/turnqueue"
)

func TestTurnQueueStoreCallerParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		action string
	}
	type expect struct {
		failure       bool
		legacyPartial bool
		q1Status      string
		q1Seq, q2Seq  int64
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"reader returns ordered queue", input{action: "list"}, expect{q1Status: "queued", q1Seq: 2, q2Seq: 1}},
		{"single status patch preserves links", input{action: "status"}, expect{q1Status: "completed", q1Seq: 2, q2Seq: 1}},
		{"two sequence patches use one invocation", input{action: "swap"}, expect{q1Status: "queued", q1Seq: 1, q2Seq: 2}},
		{"late insert rejection rolls back first patch", input{action: "lateFailure"}, expect{failure: true, legacyPartial: true, q1Status: "queued", q1Seq: 2, q2Seq: 1}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, oldPath := queueParityFixture(t, project)
			db, _ := queueParityFixture(t, project)
			rt, _ := queueParityRuntime(t, db)
			store := &queue.Store{Invoker: rt}
			var body string
			var err error
			switch tc.input.action {
			case "status":
				body = `{"data":[{"id":"q1","status":"completed"}]}`
				row := &write.TurnQueue{}
				row.SetId("q1")
				row.SetStatus("completed")
				err = store.Patch(context.Background(), row)
			case "swap":
				body = `{"data":[{"id":"q1","queueSeq":1},{"id":"q2","queueSeq":2}]}`
				first, second := &write.TurnQueue{}, &write.TurnQueue{}
				one, two := int64(1), int64(2)
				first.SetId("q1")
				first.SetQueueSeq(&one)
				second.SetId("q2")
				second.SetQueueSeq(&two)
				err = store.PatchMany(context.Background(), []*write.TurnQueue{first, second})
			case "lateFailure":
				body = `{"data":[{"id":"q1","status":"completed"},{"id":"q-reject","conversationId":"c1","turnId":"t3","messageId":"m3","queueSeq":3}]}`
				first, second := &write.TurnQueue{}, &write.TurnQueue{}
				seq := int64(3)
				first.SetId("q1")
				first.SetStatus("completed")
				second.SetId("q-reject")
				second.SetConversationId("c1")
				second.SetTurnId("t3")
				second.SetMessageId("m3")
				second.SetQueueSeq(&seq)
				err = store.PatchMany(context.Background(), []*write.TurnQueue{first, second})
			}
			if (err != nil) != tc.expect.failure {
				t.Fatalf("native failure=%v, want %v", err, tc.expect.failure)
			}
			payload, e := json.Marshal(map[string]any{"Component": "turnQueue", "DBPath": oldPath, "Body": body, "Filters": map[string]any{}})
			must(t, e)
			cmd := exec.Command(legacy)
			cmd.Stdin = bytes.NewReader(payload)
			raw, e := cmd.Output()
			must(t, e)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed != tc.expect.failure {
				t.Fatalf("legacy failure=%v (%s), want %v", before.Failed, before.Error, tc.expect.failure)
			}
			rows, e := store.List(context.Background(), &read.QueueRowsInput{})
			must(t, e)
			if len(rows) != 2 || rows[0].Id != "q2" && tc.input.action != "swap" {
				t.Fatalf("queue list=%+v", rows)
			}
			newStatus, newQ1, newQ2 := queueStoreSnapshot(t, db)
			if newStatus != tc.expect.q1Status || newQ1 != tc.expect.q1Seq || newQ2 != tc.expect.q2Seq {
				t.Fatalf("native queue=(%s,%d,%d), want %+v", newStatus, newQ1, newQ2, tc.expect)
			}
			oldStatus, oldQ1, oldQ2 := queueStoreSnapshot(t, oldDB)
			if tc.expect.legacyPartial {
				if oldStatus != "completed" || newStatus != "queued" {
					t.Fatalf("legacy partial write=%s, native rollback=%s", oldStatus, newStatus)
				}
			} else if oldStatus != newStatus || oldQ1 != newQ1 || oldQ2 != newQ2 {
				t.Fatalf("legacy queue=(%s,%d,%d), native=(%s,%d,%d)", oldStatus, oldQ1, oldQ2, newStatus, newQ1, newQ2)
			}
		})
	}
}

func queueStoreSnapshot(t *testing.T, db *sql.DB) (string, int64, int64) {
	t.Helper()
	var status string
	var first, second int64
	must(t, db.QueryRow("SELECT status,queue_seq FROM turn_queue WHERE id='q1'").Scan(&status, &first))
	must(t, db.QueryRow("SELECT queue_seq FROM turn_queue WHERE id='q2'").Scan(&second))
	return status, first, second
}
