package forecastbinding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	documents "github.com/viant/agently-core/app/store/reportingevidence"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/runtime/evidence"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

// ReportCommandBackend is shared by authenticated Begin/Complete and the pure
// compiler service. Constructing it does not enable agent/startup enforcement.
type ReportCommandBackend struct {
	runtime *Runtime
	store   *NativeSourceStore
}

func NewReportCommandBackend(runtime *Runtime, store *NativeSourceStore) (*ReportCommandBackend, error) {
	if runtime == nil || store == nil || runtime.store != store {
		return nil, reject("report command dependencies mismatch")
	}
	return &ReportCommandBackend{runtime: runtime, store: store}, nil
}

type artifactProof struct {
	Version     int    `json:"version"`
	CommandHash string `json:"commandHash"`
	SpecHash    string `json:"specHash"`
	FillHash    string `json:"fillHash"`
	PrintHash   string `json:"printHash"`
}
type compileReceiptKey struct{}
type compileReceipt struct{ ref, hash string }

func (b *ReportCommandBackend) resolve(ctx context.Context, conversation, ref string) (*CommandReceipt, error) {
	address, err := parseCommandRef(ref)
	if err != nil {
		return nil, err
	}
	if conversation == "" || address.ConversationID != conversation {
		return nil, reject("command conversation mismatch")
	}
	scope := Scope{OwnerID: authctx.EffectiveUserID(ctx), ConversationID: conversation, TurnID: address.TurnID}
	receipt, err := b.store.loadCommand(ctx, scope, address.RequestID)
	if err != nil {
		return nil, err
	}
	if commandRef(receipt) != ref {
		return nil, reject("command reference substitution")
	}
	run, err := b.store.scopedRun(ctx, scope)
	if err != nil {
		return nil, err
	}
	if run.Status != "running" && run.Status != "completed" && run.Status != "succeeded" {
		return nil, reject("command execution is not live or successfully completed")
	}
	call, err := b.store.loadNativeCall(ctx, scope, receipt.OpID, receipt.MessageID, false)
	if err != nil {
		return nil, err
	}
	if mcpname.Display(call.Tool) != "ui/report/run" || (call.Status != "running" && call.Status != "completed") {
		return nil, reject("command source operation no longer valid")
	}
	requestHash, err := RequestHash(call.Request)
	if err != nil || requestHash != receipt.RequestHash {
		return nil, reject("command source request changed")
	}
	workspaceRaw, _ := json.Marshal(receipt.Workspace)
	workspace, err := b.store.canonicalCommandWorkspace(ctx, scope, evidence.ReportCommandTarget{WindowID: receipt.WindowID, Workspace: workspaceRaw})
	if err != nil {
		return nil, err
	}
	confirmed, _ := json.Marshal(workspace)
	if !bytes.Equal(workspaceRaw, confirmed) {
		return nil, reject("command workspace changed")
	}
	builder, _ := workspace.Content.Parameters["reportBuilderRef"].(string)
	if builder == "" || builder != receipt.BuilderRef {
		return nil, reject("command builder mismatch")
	}
	seen := map[string]bool{}
	for _, id := range receipt.PlanIDs {
		if seen[id] {
			return nil, reject("duplicate command plan")
		}
		seen[id] = true
		plan, err := b.store.LoadPlan(ctx, scope, id)
		if err != nil {
			return nil, err
		}
		if plan == nil || plan.Admission.Scope != scope || plan.ID != id {
			return nil, reject("command plan scope mismatch")
		}
		if err = plan.ValidateIdentity(); err != nil {
			return nil, err
		}
	}
	return receipt, nil
}

func (b *ReportCommandBackend) AdmitReport(ctx context.Context, input evidence.ReportAdmissionInput) (json.RawMessage, error) {
	receipt, err := b.resolve(ctx, input.ConversationID, input.AdmissionRef)
	if err != nil {
		return nil, err
	}
	if receipt.RequestID != input.RequestID || receipt.BuilderRef != input.BuilderRef {
		return nil, reject("report command request or builder substitution")
	}
	return json.Marshal(commandLink{Version: 1, Ref: input.AdmissionRef, RequestID: input.RequestID})
}

func (b *ReportCommandBackend) CompileContext(ctx context.Context, ref, reportID string) (context.Context, error) {
	address, err := parseCommandRef(ref)
	if err != nil {
		return ctx, err
	}
	conversation := requestctx.ConversationIDFromContext(ctx)
	if conversation != "" && conversation != address.ConversationID {
		return ctx, reject("compiler command conversation mismatch")
	}
	receipt, err := b.resolve(ctx, address.ConversationID, ref)
	if err != nil {
		return ctx, err
	}
	if reportID != receipt.RequestID {
		return ctx, reject("compiler command report identity mismatch")
	}
	ctx = requestctx.WithConversationID(ctx, receipt.ConversationID)
	ctx = requestctx.WithTurnMeta(ctx, requestctx.TurnMeta{ConversationID: receipt.ConversationID, TurnID: receipt.TurnID})
	guard := newGuard(b.runtime, receipt.Scope, func(ctx context.Context) error { return checkPrincipal(ctx, receipt.Scope) }, func() bool { return true })
	guard.allowedPlans = map[string]bool{}
	for _, id := range receipt.PlanIDs {
		guard.allowedPlans[id] = true
	}
	body, _ := json.Marshal(receipt)
	hash, _ := RequestHash(body)
	ctx = context.WithValue(ctx, compileReceiptKey{}, compileReceipt{ref: ref, hash: hash})
	return evidence.WithPublication(ctx, guard), nil
}

func makeArtifactProof(receipt *CommandReceipt, artifacts evidence.ReportArtifacts) (*artifactProof, error) {
	raw, _ := json.Marshal(receipt)
	commandHash, err := RequestHash(raw)
	if err != nil {
		return nil, err
	}
	spec, err := ArtifactHash(artifacts.Spec)
	if err != nil {
		return nil, err
	}
	fill, err := ArtifactHash(artifacts.Fill)
	if err != nil {
		return nil, err
	}
	print, err := ArtifactHash(artifacts.Print)
	if err != nil {
		return nil, err
	}
	return &artifactProof{Version: 1, CommandHash: commandHash, SpecHash: spec, FillHash: fill, PrintHash: print}, nil
}

func (b *ReportCommandBackend) RecordCompiled(ctx context.Context, ref string, artifacts evidence.ReportArtifacts) error {
	admitted, ok := ctx.Value(compileReceiptKey{}).(compileReceipt)
	if !ok || admitted.ref != ref {
		return reject("artifact proof requires the admitted server compiler context")
	}
	address, err := parseCommandRef(ref)
	if err != nil {
		return err
	}
	receipt, err := b.resolve(ctx, address.ConversationID, ref)
	if err != nil {
		return err
	}
	proof, err := makeArtifactProof(receipt, artifacts)
	if err != nil {
		return err
	}
	if proof.CommandHash != admitted.hash {
		return reject("compiler receipt identity changed")
	}
	var spec struct {
		Source struct {
			ContainerID string `json:"containerId"`
		} `json:"source"`
	}
	if err = json.Unmarshal(artifacts.Spec, &spec); err != nil || (spec.Source.ContainerID != receipt.BuilderRef && spec.Source.ContainerID != receipt.RequestID) {
		return reject("compiled artifact source differs from command")
	}
	doc, err := b.store.documentScope(ctx, receipt.Scope)
	if err != nil {
		return err
	}
	expected, err := json.Marshal(proof)
	if err == nil {
		expected, err = canonical(expected)
	}
	if err != nil {
		return err
	}
	existing, err := b.store.documents.Load(ctx, doc, documents.Artifact, receipt.RequestID)
	if err == nil {
		if !bytes.Equal(existing, expected) {
			return reject("command already compiled different artifacts")
		}
		return nil
	}
	if !errors.Is(err, documents.ErrNotFound) {
		return err
	}
	run, err := b.store.scopedRun(ctx, receipt.Scope)
	if err != nil {
		return err
	}
	if run.LeaseOwner == nil {
		return reject("compiler execution lease unavailable")
	}
	if err = b.store.documents.Save(ctx, doc, documents.Artifact, receipt.RequestID, *run.LeaseOwner, expected); err != nil {
		return err
	}
	confirmed, err := b.store.documents.Load(ctx, doc, documents.Artifact, receipt.RequestID)
	if err != nil {
		return err
	}
	if !bytes.Equal(expected, confirmed) {
		return reject("artifact proof save unconfirmed")
	}
	return nil
}

func (b *ReportCommandBackend) VerifyReport(ctx context.Context, conversation string, linkage json.RawMessage, artifacts evidence.ReportArtifacts) error {
	var link commandLink
	decoder := json.NewDecoder(bytes.NewReader(linkage))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&link); err != nil || link.Version != 1 || link.Ref == "" || link.RequestID == "" {
		return reject("invalid stored report command linkage")
	}
	receipt, err := b.resolve(ctx, conversation, link.Ref)
	if err != nil {
		return err
	}
	if link.RequestID != receipt.RequestID {
		return reject("stored report command request mismatch")
	}
	proof, err := makeArtifactProof(receipt, artifacts)
	if err != nil {
		return err
	}
	doc, err := b.store.documentScope(ctx, receipt.Scope)
	if err != nil {
		return err
	}
	expected, err := b.store.documents.Load(ctx, doc, documents.Artifact, receipt.RequestID)
	if err != nil {
		return err
	}
	actual, _ := json.Marshal(proof)
	actual, err = canonical(actual)
	if err != nil {
		return err
	}
	if !bytes.Equal(expected, actual) {
		return reject("report artifacts differ from verified compiler output")
	}
	return nil
}
