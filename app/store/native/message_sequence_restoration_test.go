package native_test

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/stretchr/testify/require"
	datasvc "github.com/viant/agently-core/app/store/data"
	m "github.com/viant/agently-core/internal/datly/message/write"
	convstore "github.com/viant/agently-core/internal/store/conversation"
	msgmodel "github.com/viant/agently-core/model/message"
	"github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"reflect"
	"testing"
	"time"
)

func TestMessageOriginalSequenceRestoration(t *testing.T) {
	s, _, db := orphanFixture(t)
	runSequenceRestoration(t, s.Invoker, db, fmt.Sprintf("seq-%d-", time.Now().UnixNano()))
}
func runSequenceRestoration(t *testing.T, invoker exec.ComponentInvoker, db *sql.DB, p string) {
	ctx := context.Background()
	cid := p + "c"
	turn := p + "t"
	_, e := db.Exec("INSERT INTO conversation(id,created_at) VALUES(?,CURRENT_TIMESTAMP)", cid)
	require.NoError(t, e)
	_, e = db.Exec("INSERT INTO turn(id,conversation_id,created_at,status) VALUES(?,?,CURRENT_TIMESTAMP,'succeeded')", turn, cid)
	require.NoError(t, e)
	target := exec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[m.WriterComponent]().PkgPath(), Name: "CoreWrite"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/message/write"}}
	makeRow := func(id string) *m.Message {
		r := &m.Message{}
		r.SetId(p + id)
		r.SetConversationId(cid)
		r.SetTurnId(&turn)
		r.SetRole("assistant")
		r.SetType("text")
		return r
	}
	patch := func(rows ...*m.Message) error {
		_, e := invoker.InvokeComponent(ctx, exec.ComponentRequest{Target: target, Input: &m.Input{Messages: rows}})
		return e
	}
	get := func(id string) int {
		var n int
		require.NoError(t, db.QueryRow("SELECT sequence FROM message WHERE id=?", p+id).Scan(&n))
		return n
	}
	require.NoError(t, patch(makeRow("m1"), makeRow("m2")))
	require.Equal(t, 1, get("m1"))
	require.Equal(t, 2, get("m2"))
	// Emulate an independent process advancing the unique scope after the cached seed.
	_, e = db.Exec("INSERT INTO message(id,conversation_id,turn_id,role,type,sequence,created_at) VALUES(?,?,?,'assistant','text',3,CURRENT_TIMESTAMP)", p+"external", cid, turn)
	require.NoError(t, e)
	require.NoError(t, patch(makeRow("retry")))
	require.Equal(t, 4, get("retry"), "generated collision must replay original insert")
	zero := 0
	explicit := makeRow("zero")
	explicit.SetSequence(&zero)
	require.NoError(t, patch(explicit))
	require.Equal(t, 0, get("zero"))
	update := makeRow("m1")
	content := "updated"
	update.SetContent(&content)
	require.NoError(t, patch(update))
	require.Equal(t, 1, get("m1"), "ordinary update must not allocate")
	conflict := makeRow("explicit-conflict")
	one := 1
	conflict.SetSequence(&one)
	require.Error(t, patch(conflict))
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM message WHERE id=?", conflict.Id).Scan(&count))
	require.Zero(t, count)
	null := makeRow("null")
	null.SetSequence(nil)
	require.NoError(t, patch(null))
	require.Equal(t, 5, get("null"))
	// Exhaust ten allocations against competing values. The failed row stays absent.
	for n := 6; n <= 15; n++ {
		_, e = db.Exec("INSERT INTO message(id,conversation_id,turn_id,role,type,sequence,created_at) VALUES(?,?,?,'assistant','text',?,CURRENT_TIMESTAMP)", fmt.Sprintf("%sexternal%d", p, n), cid, turn, n)
		require.NoError(t, e)
	}
	require.Error(t, patch(makeRow("exhausted")))
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM message WHERE id=?", p+"exhausted").Scan(&count))
	require.Zero(t, count)
	atomicConflict := makeRow("atomic-conflict")
	atomicConflict.SetSequence(&one)
	require.Error(t, patch(makeRow("atomic-first"), atomicConflict))
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM message WHERE id=?", p+"atomic-first").Scan(&count))
	require.Zero(t, count, "native batch must rollback earlier generated row")

	require.NoError(t, patch(makeRow("after")))
	require.Equal(t, 17, get("after"), "rolled-back allocations retain original counter gaps")
	public := msgmodel.NewMutableMessageView(msgmodel.WithMessageID(p+"data"), msgmodel.WithMessageConversationID(cid), msgmodel.WithMessageTurnID(turn), msgmodel.WithMessageRole("assistant"), msgmodel.WithMessageType("text"))
	returned, err := datasvc.NewService(invoker).PatchMessages(ctx, []*msgmodel.MutableMessageView{public})
	require.NoError(t, err)
	require.Len(t, returned, 1)
	require.Same(t, public, returned[0])
	require.NotNil(t, public.Sequence)
	require.Equal(t, 18, *public.Sequence)
	require.Equal(t, 18, get("data"))
	trusted := makeRow("trusted")
	store := &convstore.MessageStore{Invoker: invoker}
	require.NoError(t, store.PatchTrusted(ctx, trusted))
	require.Equal(t, 19, get("trusted"))
	_, err = db.Exec("INSERT INTO message(id,conversation_id,turn_id,role,type,sequence,created_at) VALUES(?,?,?,'assistant','text',20,CURRENT_TIMESTAMP)", p+"data-external", cid, turn)
	require.NoError(t, err)
	replayPublic := msgmodel.NewMutableMessageView(msgmodel.WithMessageID(p+"data-replay"), msgmodel.WithMessageConversationID(cid), msgmodel.WithMessageTurnID(turn), msgmodel.WithMessageRole("assistant"), msgmodel.WithMessageType("text"))
	returned, err = datasvc.NewService(invoker).PatchMessages(ctx, []*msgmodel.MutableMessageView{replayPublic})
	require.NoError(t, err)
	require.Len(t, returned, 1)
	require.Same(t, replayPublic, returned[0])
	require.NotNil(t, replayPublic.Sequence)
	require.Equal(t, 21, *replayPublic.Sequence)
	require.Equal(t, 21, get("data-replay"))
	seededTurn := p + "seeded-turn"
	_, err = db.Exec("INSERT INTO turn(id,conversation_id,status,created_at) VALUES(?,?,'succeeded',CURRENT_TIMESTAMP)", seededTurn, cid)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO message(id,conversation_id,turn_id,role,type,sequence,created_at) VALUES(?,?,?,'assistant','text',42,CURRENT_TIMESTAMP)", p+"seed", cid, seededTurn)
	require.NoError(t, err)
	seeded := makeRow("seeded-next")
	seeded.SetTurnId(&seededTurn)
	require.NoError(t, patch(seeded))
	require.Equal(t, 43, get("seeded-next"), "first allocation seeds highest persisted turn sequence")
}

func TestMessageOriginalSequenceRestorationMySQL(t *testing.T) {
	s, _, db, p := orphanMySQLFixture(t)
	var n int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='sqlx_scoped_sequences'").Scan(&n))
	require.Zero(t, n, "original schema has no allocation ledger")
	runSequenceRestoration(t, s.Invoker, db, p+"sequence-")
}

func TestMessageSequenceConcurrentMySQL(t *testing.T) {
	first, _, db, p := orphanMySQLFixture(t)
	second, _, _, _ := orphanMySQLFixture(t)
	ctx := context.Background()
	cid := p + "parallel-c"
	turn := p + "parallel-t"
	other := p + "other-t"
	_, e := db.Exec("INSERT INTO conversation(id,created_at) VALUES(?,CURRENT_TIMESTAMP)", cid)
	require.NoError(t, e)
	for _, id := range []string{turn, other} {
		_, e = db.Exec("INSERT INTO turn(id,conversation_id,created_at,status) VALUES(?,?,CURRENT_TIMESTAMP,'succeeded')", id, cid)
		require.NoError(t, e)
	}
	target := exec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[m.WriterComponent]().PkgPath(), Name: "CoreWrite"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/message/write"}}
	done := make(chan error, 12)
	for n := 0; n < 12; n++ {
		go func(n int) {
			r := &m.Message{}
			r.SetId(fmt.Sprintf("%sparallel%d", p, n))
			r.SetConversationId(cid)
			r.SetTurnId(&turn)
			r.SetRole("assistant")
			r.SetType("text")
			invoker := first.Invoker
			if n%2 == 1 {
				invoker = second.Invoker
			}
			_, e := invoker.InvokeComponent(ctx, exec.ComponentRequest{Target: target, Input: &m.Input{Messages: []*m.Message{r}}})
			done <- e
		}(n)
	}
	for n := 0; n < 12; n++ {
		require.NoError(t, <-done)
	}
	var count, min, max int
	require.NoError(t, db.QueryRow("SELECT COUNT(DISTINCT sequence),MIN(sequence),MAX(sequence) FROM message WHERE turn_id=?", turn).Scan(&count, &min, &max))
	require.Equal(t, 12, count)
	require.Equal(t, 1, min)
	require.Equal(t, 12, max)
	r := &m.Message{}
	r.SetId(p + "independent")
	r.SetConversationId(cid)
	r.SetTurnId(&other)
	r.SetRole("assistant")
	r.SetType("text")
	_, e = second.Invoker.InvokeComponent(ctx, exec.ComponentRequest{Target: target, Input: &m.Input{Messages: []*m.Message{r}}})
	require.NoError(t, e)
	require.NoError(t, db.QueryRow("SELECT sequence FROM message WHERE id=?", r.Id).Scan(&min))
	require.Equal(t, 1, min)
}
