package reportingrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	convstore "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	authctx "github.com/viant/agently-core/internal/auth"
	conversationsql "github.com/viant/agently-core/internal/service/conversation"
	authsvc "github.com/viant/agently-core/service/auth"
	reportingrunsvc "github.com/viant/agently-core/service/reportingrun"
)

type conversationStub struct {
	convstore.Client
	ownerID        string
	conversationID string
}

func (s *conversationStub) GetConversation(ctx context.Context, id string, _ ...convstore.Option) (*convstore.Conversation, error) {
	if authctx.EffectiveUserID(ctx) != s.ownerID || id != s.conversationID {
		return nil, reportstore.ErrNotFound
	}
	ownerID := s.ownerID
	return &convstore.Conversation{CreatedByUserId: &ownerID}, nil
}

func newHandlerTestSubject(t *testing.T) (http.Handler, reportstore.RunClient) {
	t.Helper()
	store, ok := reportmemory.New().(reportstore.RunClient)
	if !ok {
		t.Fatal("memory reporting store does not implement RunClient")
	}
	service := reportingrunsvc.New(reportingrunsvc.Options{
		Store: store,
		NewID: func() string {
			return "server-run-1"
		},
	})
	return New(service, &conversationStub{ownerID: "owner-1", conversationID: "conv-1"}, false), store
}

func postJSON(t *testing.T, handler http.Handler, path string, body interface{}, ownerID string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	if ownerID != "" {
		request = request.WithContext(authsvc.InjectUser(request.Context(), ownerID))
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestHandler_RequiresAuthenticationAndTrustedConversation(t *testing.T) {
	handler, store := newHandlerTestSubject(t)
	input := map[string]interface{}{
		"uiRunRequestId": "transport-1",
		"conversationId": "conv-1",
		"origin":         "prompt",
	}

	if response := postJSON(t, handler, routePrefix+"/begin", input, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	input["conversationId"] = "conv-other"
	if response := postJSON(t, handler, routePrefix+"/begin", input, "owner-1"); response.Code != http.StatusNotFound {
		t.Fatalf("untrusted conversation status = %d, want %d", response.Code, http.StatusNotFound)
	}
	ownerCtx := authsvc.InjectUser(context.Background(), "owner-1")
	if _, err := store.GetReportRunByRequestID(ownerCtx, "transport-1"); err == nil {
		t.Fatal("untrusted conversation created a report run")
	}

	input["conversationId"] = "conv-1"
	response := postJSON(t, handler, routePrefix+"/begin", input, "owner-1")
	if response.Code != http.StatusOK {
		t.Fatalf("trusted begin status = %d, body = %s", response.Code, response.Body.String())
	}
	var result reportingrunsvc.BeginResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Run == nil || result.Run.ReportRunID != "server-run-1" || result.Run.OwnerID != "owner-1" {
		t.Fatalf("trusted begin result = %+v", result.Run)
	}
}

func TestHandler_AdoptionIsSeparatelyDefaultClosed(t *testing.T) {
	handler, _ := newHandlerTestSubject(t)
	response := postJSON(t, handler, routePrefix+"/server-run-1/adopt", map[string]interface{}{
		"conversationId": "conv-1",
	}, "owner-1")
	if response.Code != http.StatusNotFound {
		t.Fatalf("adoption status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestHandler_ProductionSQLConversationClientEnforcesExactOwner(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	conversations, err := conversationsql.New(ctx, dao)
	if err != nil {
		t.Fatalf("conversation.New() error = %v", err)
	}
	connector, err := dao.Resource().Connector("agently")
	if err != nil {
		t.Fatalf("Connector() error = %v", err)
	}
	db, err := connector.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	for _, row := range []struct {
		id      string
		ownerID interface{}
	}{
		{id: "conv-owner-1", ownerID: "owner-1"},
		{id: "conv-owner-2", ownerID: "owner-2"},
		{id: "conv-ownerless", ownerID: nil},
	} {
		if _, err := db.ExecContext(ctx, `
INSERT INTO conversation (id, created_by_user_id, visibility, shareable)
VALUES (?, ?, 'private', 0)`, row.id, row.ownerID); err != nil {
			t.Fatalf("insert conversation %s error = %v", row.id, err)
		}
	}
	store := reportmemory.New().(reportstore.RunClient)
	service := reportingrunsvc.New(reportingrunsvc.Options{Store: store})
	handler := New(service, conversations, false)

	owned := postJSON(t, handler, routePrefix+"/begin", map[string]interface{}{
		"uiRunRequestId": "owned-request",
		"conversationId": "conv-owner-1",
		"origin":         "prompt",
	}, "owner-1")
	if owned.Code != http.StatusOK {
		t.Fatalf("owned conversation status = %d body=%s", owned.Code, owned.Body.String())
	}
	for _, conversationID := range []string{"conv-owner-2", "conv-ownerless", "conv-missing"} {
		response := postJSON(t, handler, routePrefix+"/begin", map[string]interface{}{
			"uiRunRequestId": "denied-" + conversationID,
			"conversationId": conversationID,
			"origin":         "prompt",
		}, "owner-1")
		if response.Code != http.StatusNotFound {
			t.Fatalf("conversation %s status = %d, want %d; body=%s", conversationID, response.Code, http.StatusNotFound, response.Body.String())
		}
		ownerCtx := authsvc.InjectUser(ctx, "owner-1")
		if _, err := store.GetReportRunByRequestID(ownerCtx, "denied-"+conversationID); !errors.Is(err, reportstore.ErrNotFound) {
			t.Fatalf("denied conversation %s persisted a run: %v", conversationID, err)
		}
	}
}
