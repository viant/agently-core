package sdk

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/runtime/aguistate"
)

type identityCountingStore struct {
	aguistore.Store
	calls map[string]int
	read  func(context.Context, string, string, string) (*aguistore.Run, error)
}

func (s *identityCountingStore) GetRun(ctx context.Context, principal, thread, id string) (*aguistore.Run, error) {
	s.calls[id]++
	return s.read(ctx, principal, thread, id)
}

func identityJournalPair(run *aguistore.Run) []aguistate.Object {
	identity := aguistate.Object{"version": "1", "protocolRunId": run.RunID, "nativeTurnId": run.TurnID, "clientMessageId": run.ClientMessageID}
	return []aguistate.Object{
		{"role": "user", "id": run.ClientMessageID, "metadata": aguistate.Object{"agently": aguistate.Object{"presentation": identity}}},
		{"role": "activity", "activityType": "agently.user-identity", "content": identity},
	}
}

func TestAGUIBootstrapUserAliasesReadsEachSuccessfulRunOnce(t *testing.T) {
	// The long conversation journal has 14 runs, each represented by both a
	// user presentation and a durable identity activity (28 identity entries).
	record := &aguistore.Run{Principal: "owner", ThreadID: "thread"}
	transcript := &ConversationStateResponse{Conversation: &ConversationState{}}
	var journal []aguistate.Object
	expected := map[string]string{}
	runs := map[string]*aguistore.Run{}
	for i := 0; i < 14; i++ {
		run := &aguistore.Run{Principal: record.Principal, ThreadID: record.ThreadID, RunID: fmt.Sprint("run-", i), TurnID: fmt.Sprint("turn-", i), ClientMessageID: fmt.Sprint("client-", i)}
		runs[run.RunID] = run
		native := fmt.Sprint("native-", i)
		transcript.Conversation.Turns = append(transcript.Conversation.Turns, &TurnState{TurnID: run.TurnID, StartedByMessageID: native})
		expected[native] = run.ClientMessageID
		journal = append(journal, identityJournalPair(run)...)
	}
	store := &identityCountingStore{calls: map[string]int{}, read: func(_ context.Context, principal, thread, id string) (*aguistore.Run, error) {
		if principal != record.Principal || thread != record.ThreadID {
			t.Fatal("lookup lost ownership scope")
		}
		return runs[id], nil
	}}
	for request := 0; request < 2; request++ {
		if got := aguiBootstrapUserAliases(context.Background(), store, record, transcript, journal); !reflect.DeepEqual(got, expected) {
			t.Fatalf("aliases = %v, want %v", got, expected)
		}
		for id := range runs {
			if store.calls[id] != request+1 {
				t.Fatalf("run %s reads = %d, want %d", id, store.calls[id], request+1)
			}
		}
	}
}

func TestAGUIBootstrapUserAliasesRetainsIdentityChecksAndOrder(t *testing.T) {
	record := &aguistore.Run{Principal: "owner", ThreadID: "thread"}
	good := &aguistore.Run{Principal: "owner", ThreadID: "thread", RunID: "run", TurnID: "turn", ClientMessageID: "client"}
	transcript := &ConversationStateResponse{Conversation: &ConversationState{Turns: []*TurnState{{TurnID: "turn", StartedByMessageID: "native"}}}}
	for _, field := range []string{"Principal", "ThreadID", "TurnID", "ClientMessageID"} {
		t.Run(field, func(t *testing.T) {
			invalid := *good
			reflect.ValueOf(&invalid).Elem().FieldByName(field).SetString("foreign")
			store := &identityCountingStore{calls: map[string]int{}, read: func(context.Context, string, string, string) (*aguistore.Run, error) { return &invalid, nil }}
			if got := aguiBootstrapUserAliases(context.Background(), store, record, transcript, identityJournalPair(good)); len(got) != 0 {
				t.Fatalf("accepted %s mismatch: %v", field, got)
			}
		})
	}
	// A later identity for the same run cannot replace its checked turn/client;
	// a later valid run for the same native starter retains journal ordering.
	wrong := *good
	wrong.ClientMessageID = "forged"
	later := *good
	later.RunID = "later"
	later.ClientMessageID = "later-client"
	badUser := identityJournalPair(good)[0]
	badUser["id"] = "forged"
	journal := []aguistate.Object{badUser}
	journal = append(journal, identityJournalPair(good)...)
	journal = append(journal, identityJournalPair(&wrong)...)
	journal = append(journal, identityJournalPair(&later)...)
	store := &identityCountingStore{calls: map[string]int{}, read: func(_ context.Context, _, _, id string) (*aguistore.Run, error) {
		if id == "later" {
			return &later, nil
		}
		return good, nil
	}}
	if got := aguiBootstrapUserAliases(context.Background(), store, record, transcript, journal); !reflect.DeepEqual(got, map[string]string{"native": "later-client"}) {
		t.Fatalf("aliases/order changed: %v", got)
	}
	if store.calls["run"] != 1 || store.calls["later"] != 1 {
		t.Fatalf("reads: %v", store.calls)
	}
}

func TestAGUIBootstrapUserAliasesRetriesFailedReads(t *testing.T) {
	run := &aguistore.Run{Principal: "owner", ThreadID: "thread", RunID: "run", TurnID: "turn", ClientMessageID: "client"}
	transcript := &ConversationStateResponse{Conversation: &ConversationState{Turns: []*TurnState{{TurnID: "turn", StartedByMessageID: "native"}}}}
	for _, failure := range []string{"error", "nil"} {
		t.Run(failure, func(t *testing.T) {
			reads := 0
			store := &identityCountingStore{calls: map[string]int{}, read: func(context.Context, string, string, string) (*aguistore.Run, error) {
				reads++
				if reads == 1 {
					if failure == "error" {
						return nil, errors.New("read failed")
					}
					return nil, nil
				}
				return run, nil
			}}
			got := aguiBootstrapUserAliases(context.Background(), store, run, transcript, identityJournalPair(run))
			if reads != 2 || got["native"] != "client" {
				t.Fatalf("failed read retry changed: reads=%d aliases=%v", reads, got)
			}
		})
	}
}

func TestAGUIBootstrapUserAliasesChecksEveryRepeatedIdentity(t *testing.T) {
	run := &aguistore.Run{Principal: "owner", ThreadID: "thread", RunID: "run", TurnID: "turn", ClientMessageID: "client"}
	transcript := &ConversationStateResponse{Conversation: &ConversationState{Turns: []*TurnState{{TurnID: "turn", StartedByMessageID: "native"}, {TurnID: "other-turn", StartedByMessageID: "other-native"}}}}
	for _, field := range []string{"nativeTurnId", "clientMessageId"} {
		t.Run(field, func(t *testing.T) {
			journal := identityJournalPair(run)
			forged := aguistate.Object{"version": "1", "protocolRunId": "run", "nativeTurnId": "turn", "clientMessageId": "client"}
			forged[field] = "other-" + map[string]string{"nativeTurnId": "turn", "clientMessageId": "client"}[field]
			journal = append(journal, aguistate.Object{"role": "activity", "activityType": "agently.user-identity", "content": forged})
			store := &identityCountingStore{calls: map[string]int{}, read: func(context.Context, string, string, string) (*aguistore.Run, error) { return run, nil }}
			if got := aguiBootstrapUserAliases(context.Background(), store, run, transcript, journal); !reflect.DeepEqual(got, map[string]string{"native": "client"}) {
				t.Fatalf("repeated forged identity accepted: %v", got)
			}
			if store.calls["run"] != 1 {
				t.Fatalf("reads: %v", store.calls)
			}
		})
	}
	user := identityJournalPair(run)[0]
	user["id"] = "different-user"
	store := &identityCountingStore{calls: map[string]int{}, read: func(context.Context, string, string, string) (*aguistore.Run, error) { return run, nil }}
	if got := aguiBootstrapUserAliases(context.Background(), store, run, transcript, []aguistate.Object{user}); len(got) != 0 {
		t.Fatalf("unmatched user accepted: %v", got)
	}
}
