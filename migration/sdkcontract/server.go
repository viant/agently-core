// Package sdkcontract builds a disposable application server for SDK transport tests.
package sdkcontract

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/viant/agently-core/app/executor"
	appserver "github.com/viant/agently-core/app/server"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	messagemodel "github.com/viant/agently-core/model/message"
	turnmodel "github.com/viant/agently-core/model/turn"
	turnqueuemodel "github.com/viant/agently-core/model/turnqueue"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/sdk"
	svcauth "github.com/viant/agently-core/service/auth"
	reporting "github.com/viant/agently-core/service/reporting"
	"github.com/viant/agently-core/service/scheduler"
)

const Owner = "sdk-contract-owner"
const ConversationID = "sdk-contract-conversation"
const QueueConversationID = "sdk-contract-queue"

type Server struct {
	Runtime            *executor.Runtime
	Backend            sdk.Backend
	Handler            http.Handler
	Token, OtherToken  string
	ReportingArtifacts map[string]string
}

func New(ctx context.Context, root string) (*Server, error) {
	return newServer(ctx, root, false)
}

// NewReporting enables the existing opt-in browser report lifecycle on an isolated fixture.
func NewReporting(ctx context.Context, root string) (*Server, error) {
	return newServer(ctx, root, true)
}

func newServer(ctx context.Context, root string, reportingLifecycle bool) (*Server, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	private := filepath.Join(root, "fixture-private.pem")
	public := filepath.Join(root, "fixture-public.pem")
	if err = os.WriteFile(private, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); err != nil {
		return nil, err
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(public, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}), 0600); err != nil {
		return nil, err
	}
	config := fmt.Sprintf("auth:\n  enabled: true\n  tokenEncryptionKey: sdk-contract-disposable-token-encryption\n  jwt:\n    enabled: true\n    rsa:\n      - %q\n    rsaPrivateKey: %q\ndefault:\n  reporting:\n    enabled: true\n    store:\n      backend: sql\n", public, private)
	if reportingLifecycle {
		config += "    transitionalWithUI:\n      admission: open\n      persistence: enabled\n      conversationAdoption: enabled\n      exportFromRun: enabled\n"
	}
	if err = os.WriteFile(filepath.Join(root, "config.yaml"), []byte(config), 0600); err != nil {
		return nil, err
	}
	// Build the actual runtime, then stop automatic background workers. HTTP
	// operations use their own request contexts; Runtime.Close owns the pools.
	buildCtx, stopWorkers := context.WithCancel(ctx)
	rt, backend, finder, err := appserver.BuildWorkspaceRuntime(buildCtx, appserver.RuntimeOptions{WorkspaceRoot: root})
	stopWorkers()
	if err != nil {
		return nil, err
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = rt.Close(context.Background())
		}
	}()
	auth, err := svcauth.NewRuntime(ctx, root, rt.Native)
	if err != nil {
		return nil, err
	}
	token, err := auth.JWTService().Sign(time.Hour, map[string]interface{}{"sub": Owner})
	if err != nil {
		return nil, err
	}
	other, err := auth.JWTService().Sign(time.Hour, map[string]interface{}{"sub": "sdk-contract-other"})
	if err != nil {
		return nil, err
	}
	if err = seed(svcauth.InjectUser(ctx, Owner), rt, reportingLifecycle); err != nil {
		return nil, err
	}
	reportingArtifacts := map[string]string{}
	if reportingLifecycle {
		snapshots := ReportingSnapshots()
		ownerCtx := svcauth.InjectUser(ctx, Owner)
		for _, client := range []string{"android", "swift", "ts"} {
			job, seedErr := rt.Reporting.SubmitExport(ownerCtx, &reporting.SubmitExportRequest{ArtifactRef: "report://fixture/" + client, Format: reporting.ExportFormatPDF, Scope: reporting.ExportScopeDraft, ConversationID: "sdk-contract-" + client, ReportPrint: snapshots["reportPrint"]})
			if seedErr != nil {
				return nil, seedErr
			}
			if _, seedErr = rt.Reporting.StartExport(ownerCtx, job.JobID); seedErr != nil {
				return nil, seedErr
			}
			finished, seedErr := rt.Reporting.CompleteExport(ownerCtx, &reporting.CompleteExportRequest{JobID: job.JobID, ContentType: "application/pdf", Data: []byte("%PDF deterministic SDK fixture")})
			if seedErr != nil {
				return nil, seedErr
			}
			reportingArtifacts[client] = finished.ArtifactID
		}
	}
	scheduleStore, err := scheduler.NewDatlyStore(ctx, rt.Native, rt.Data)
	if err != nil {
		return nil, err
	}
	schedulerService := scheduler.New(scheduleStore, rt.Agent, scheduler.WithConversationClient(rt.Conversation))
	handler, err := appserver.NewAPIHandler(ctx, appserver.APIOptions{Version: "native-sdk-contract", Runtime: rt, Client: backend, AgentFinder: finder, AuthRuntime: auth, SchedulerService: schedulerService, SchedulerOptions: &sdk.SchedulerOptions{EnableAPI: true}})
	if err != nil {
		return nil, err
	}
	succeeded = true
	return &Server{Runtime: rt, Backend: backend, Handler: handler, Token: token, OtherToken: other, ReportingArtifacts: reportingArtifacts}, nil
}

func seed(ctx context.Context, rt *executor.Runtime, reportingLifecycle bool) error {
	ids := []string{ConversationID, QueueConversationID, "sdk-contract-delete"}
	if reportingLifecycle {
		for _, client := range []string{"android", "swift", "ts"} {
			ids = append(ids, "sdk-contract-"+client)
		}
	}
	for _, id := range ids {
		row := &conversationmodel.MutableConversationView{}
		row.SetId(id)
		row.SetCreatedByUserID(Owner)
		row.SetTitle("Native SDK contract")
		row.SetStatus("succeeded")
		row.SetUsageInputTokens(11)
		row.SetUsageOutputTokens(7)
		if _, err := rt.Data.PatchConversations(ctx, []*conversationmodel.MutableConversationView{row}); err != nil {
			return err
		}
	}
	items := []struct{ id, conv, status string }{{"sdk-contract-turn", ConversationID, "succeeded"}, {"sdk-contract-queued-turn", QueueConversationID, "queued"}}
	if reportingLifecycle {
		for _, client := range []string{"android", "swift", "ts"} {
			items = append(items, struct{ id, conv, status string }{"sdk-contract-" + client + "-turn", "sdk-contract-" + client, "queued"})
		}
	}
	for _, item := range items {
		turn := &turnmodel.Turn{}
		turn.SetId(item.id)
		turn.SetConversationID(item.conv)
		turn.SetStatus(item.status)
		turn.SetQueueSeq(1)
		if _, err := rt.Data.PatchTurns(ctx, []*turnmodel.Turn{turn}); err != nil {
			return err
		}
		message := &messagemodel.Message{}
		message.SetId(item.id + "-message")
		message.SetConversationID(item.conv)
		message.SetTurnID(item.id)
		message.SetRole("user")
		message.SetType("text")
		message.SetContent("fixture prompt")
		message.SetInterim(0)
		if _, err := rt.Data.PatchMessages(ctx, []*messagemodel.Message{message}); err != nil {
			return err
		}
		turn.SetStartedByMessageID(message.Id)
		if _, err := rt.Data.PatchTurns(ctx, []*turnmodel.Turn{turn}); err != nil {
			return err
		}
		if item.status == "queued" {
			queue := &turnqueuemodel.TurnQueue{}
			if item.id == "sdk-contract-queued-turn" {
				queue.SetId("sdk-contract-queue-row")
			} else {
				queue.SetId(item.id + "-queue")
			}
			queue.SetConversationId(item.conv)
			queue.SetTurnId(item.id)
			queue.SetMessageId(message.Id)
			queue.SetQueueSeq(1)
			queue.SetStatus("queued")
			queueStore, ok := rt.Data.(interface {
				PatchTurnQueue(context.Context, *turnqueuemodel.TurnQueue) error
			})
			if !ok {
				return fmt.Errorf("native fixture data store lacks turn queue mutation")
			}
			if err := queueStore.PatchTurnQueue(ctx, queue); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) Publish(ctx context.Context, seq int64) error {
	return s.Runtime.Streaming.Publish(ctx, &streaming.Event{ID: fmt.Sprintf("sdk-contract-event-%d", seq), EventSeq: seq, ConversationID: ConversationID, StreamID: ConversationID, Type: streaming.EventTypeUsage, Patch: map[string]interface{}{"inputTokens": 11, "outputTokens": 7}})
}
