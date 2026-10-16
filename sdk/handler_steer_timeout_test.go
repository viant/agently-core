package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type steerAcceptanceClient struct {
	Client
	wait bool
}

func (c steerAcceptanceClient) SteerTurn(ctx context.Context, input *SteerTurnInput) (*SteerTurnOutput, error) {
	if c.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &SteerTurnOutput{TurnID: input.TurnID, MessageID: "accepted-message", Status: "accepted"}, nil
}

func TestSteerAcceptanceDoesNotWaitIndefinitelyForAStalledStore(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/conversations/c/turns/t/steer", strings.NewReader(`{"content":"more detail"}`))
	r.SetPathValue("id", "c")
	r.SetPathValue("turnId", "t")
	w := httptest.NewRecorder()
	start := time.Now()
	handleSteerTurnWithTimeout(steerAcceptanceClient{wait: true}, 20*time.Millisecond)(w, r)
	if w.Code != http.StatusGatewayTimeout || time.Since(start) > time.Second {
		t.Fatalf("stalled steer returned status %d after %s: %s", w.Code, time.Since(start), w.Body.String())
	}
}

func TestSteerAcceptanceStillReturnsAcceptedMessage(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/conversations/c/turns/t/steer", strings.NewReader(`{"content":"more detail"}`))
	r.SetPathValue("id", "c")
	r.SetPathValue("turnId", "t")
	w := httptest.NewRecorder()
	handleSteerTurnWithTimeout(steerAcceptanceClient{}, time.Second)(w, r)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), "accepted-message") {
		t.Fatalf("accepted steer returned status %d: %s", w.Code, w.Body.String())
	}
}
