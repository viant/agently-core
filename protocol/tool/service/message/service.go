package message

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	_ "embed"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/embedder"
	svc "github.com/viant/agently-core/protocol/tool/service"
	core "github.com/viant/agently-core/service/core"
)

const Name = "message"

var (
	//go:embed tools/show.md
	showDesc string
	//go:embed tools/summarize.md
	summarizeDesc string
	//go:embed tools/match.md
	matchDesc string
	//go:embed tools/remove.md
	removeDesc string
	//go:embed tools/project.md
	projectDesc string
	//go:embed tools/askUser.md
	askUserDesc string
	//go:embed tools/add.md
	addDesc string
)

// Service provides message utilities exposed to agents.
type Service struct {
	conv                                                  apiconv.Client
	core                                                  coreGen
	embedder                                              embedder.Finder
	elicitor                                              Elicitor
	summarizeChunk, matchChunk                            int
	summaryModel, summaryPrompt, defaultModel, embedModel string
}

type coreGen interface {
	Generate(ctx context.Context, input *core.GenerateInput, output *core.GenerateOutput) error
}

// New creates a basic service; summarization/match require dependencies set via options or NewWithDeps.
func New(conv apiconv.Client) *Service { return &Service{conv: conv} }

// NewWithDeps provides full dependencies for summarize/match operations.
func NewWithDeps(conv apiconv.Client, core coreGen, emb embedder.Finder, summarizeChunk, matchChunk int, summaryModel, summaryPrompt, defaultModel, embedModel string, opts ...Option) *Service {
	s := &Service{conv: conv, core: core, embedder: emb, summarizeChunk: summarizeChunk, matchChunk: matchChunk, summaryModel: summaryModel, summaryPrompt: summaryPrompt, defaultModel: defaultModel, embedModel: embedModel}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Option configures optional dependencies on the message service.
type Option func(*Service)

// WithElicitor sets the elicitation dependency for the askUser tool.
func WithElicitor(e Elicitor) Option {
	return func(s *Service) { s.elicitor = e }
}

func (s *Service) Name() string { return Name }

// CacheableMethods declares which methods produce cacheable outputs.
func (s *Service) CacheableMethods() map[string]bool {
	return map[string]bool{
		"show":           true,
		"summarize":      true,
		"match":          true,
		"listCandidates": true,
	}
}

func (s *Service) Methods() svc.Signatures {
	// Note: message:compact is intentionally NOT registered here.
	// Compaction is only used internally by the orchestrator to free space
	// for the Token‑Limit Presentation message. Normal cleanup should be
	// LLM-driven via listCandidates + remove (and optionally summarize).
	sigs := []svc.Signature{
		{Name: "add", Description: addDesc, Input: reflect.TypeOf(&AddInput{}), Output: reflect.TypeOf(&AddOutput{})},
		{Name: "show", Description: showDesc, Input: reflect.TypeOf(&ShowInput{}), Output: reflect.TypeOf(&ShowOutput{})},
		{Name: "summarize", Description: summarizeDesc, Input: reflect.TypeOf(&SummarizeInput{}), Output: reflect.TypeOf(&SummarizeOutput{})},
		{Name: "match", Description: matchDesc, Input: reflect.TypeOf(&MatchInput{}), Output: reflect.TypeOf(&MatchOutput{})},
		{Name: "listCandidates", Description: "List removable messages with byte/token size and concise preview.", Input: reflect.TypeOf(&ListCandidatesInput{}), Output: reflect.TypeOf(&ListCandidatesOutput{})},
		{Name: "remove", Description: removeDesc, Input: reflect.TypeOf(&RemoveInput{}), Output: reflect.TypeOf(&RemoveOutput{})},
		{Name: "project", Description: projectDesc, Input: reflect.TypeOf(&ProjectInput{}), Output: reflect.TypeOf(&ProjectOutput{})},
	}
	if s.elicitor != nil {
		sigs = append(sigs, svc.Signature{Name: "askUser", Description: askUserDesc, Input: reflect.TypeOf(&AskUserInput{}), Output: reflect.TypeOf(&AskUserOutput{})})
	}
	return sigs
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(name) {
	case "add":
		return s.add, nil
	case "show":
		return s.show, nil
	case "summarize":
		return s.summarize, nil
	case "match":
		return s.match, nil
	case "listcandidates":
		return s.listCandidates, nil
	case "remove":
		return s.remove, nil
	case "project":
		return s.project, nil
	case "askuser":
		if s.elicitor == nil {
			return nil, fmt.Errorf("askUser: elicitation not configured")
		}
		return s.askUser, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}
