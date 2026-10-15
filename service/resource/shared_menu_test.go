package resource

import (
	"context"
	"encoding/json"

	identity "github.com/viant/agently-core/protocol/resource"

	"strings"

	"testing"
	"time"
)

const menuFixture = `{"schemaVersion":1,"applications":[{"id":"steward","title":"Workspace","icon":"applications","menus":[{"id":"advertisers","title":"Advertisers","icon":"office","action":{"type":"window","windowKey":"advertiserList","parameters":{"active":true}}},{"id":"campaigns","title":"Campaigns","icon":"timeline-events","action":{"type":"window","windowKey":"campaignList"}},{"id":"group","title":"Hidden","menus":[{"id":"secret","action":{"type":"window","windowKey":"secretWindow"}}]}]}]}`

func menuFixtureProvider(t *testing.T) (*LocalProvider, *localFixtureSource, *identity.VerifiedActor, *bool) {
	p, source, actor, revoked := localFixture(t)
	source.raw = json.RawMessage(menuFixture)
	b := localProviderBinding(p, "window://team/sales")
	b.URI = "navigation://team/navigation"
	b.Changes = source
	b.FormatVersion = 1
	b.Title = "Navigation"
	updateLocalProvider(t, p, func(config *LocalConfig) {
		config.Bindings = []LocalResourceBinding{b}
		config.Validators = map[string]LocalResourceValidator{"navigation": ValidateMenu}
		config.MenuActionVisibility = func(_ context.Context, _ identity.VerifiedActor, _ identity.ResourceURI, raw json.RawMessage) (bool, error) {
			return !strings.Contains(string(raw), "secretWindow"), nil
		}
	})
	return p, source, actor, revoked
}

type stampedMenuSource struct{ raw json.RawMessage }

func (s *stampedMenuSource) Candidates(context.Context, identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	return []identity.ResourceCandidate{{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint(s.raw)}, {Kind: identity.StampedCandidate, Revision: "release-7", ContentFingerprint: identity.ContentFingerprint(s.raw)}}, nil
}
func (s *stampedMenuSource) ReadCandidate(context.Context, identity.ResourceURI, identity.ResourceCandidate) (json.RawMessage, error) {
	return append(json.RawMessage(nil), s.raw...), nil
}

type stampMenuPolicy struct{ until time.Time }

func (p stampMenuPolicy) SelectRevision(_ context.Context, ref identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	for _, c := range candidates {
		if c.Kind == identity.StampedCandidate && (ref.Revision == "" || ref.Revision == "release-7") {
			return identity.ResourceDecision{Candidate: c, AuthorityBinding: "source-menu-authority", ValidUntil: p.until}, nil
		}
	}
	return identity.ResourceDecision{}, identity.ErrResourceDenied
}
