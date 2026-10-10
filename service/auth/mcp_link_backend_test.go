package auth

import (
	"context"
	"net/url"
	"testing"
)

func TestMCPLinkBackendConstructorRequiresConfiguredAuthorityAndStores(t *testing.T) {
	fixture := newMCPLinkFixture(t)
	cfg, registry := fixture.service.cfg, fixture.delegated.registry
	tokens, states := fixture.delegated.resolver.store, fixture.service.states
	configs, users := fixture.service.configs, fixture.service.users
	backend, err := NewMCPLinkBackend(cfg, registry, tokens, states, configs, users)
	if err != nil || backend == nil || backend.service == nil || backend.resolver == nil {
		t.Fatalf("configured backend unavailable: %v", err)
	}
	for name, construct := range map[string]func() (*MCPLinkBackend, error){
		"configuration": func() (*MCPLinkBackend, error) {
			return NewMCPLinkBackend(nil, registry, tokens, states, configs, users)
		},
		"registry":             func() (*MCPLinkBackend, error) { return NewMCPLinkBackend(cfg, nil, tokens, states, configs, users) },
		"tokens":               func() (*MCPLinkBackend, error) { return NewMCPLinkBackend(cfg, registry, nil, states, configs, users) },
		"states":               func() (*MCPLinkBackend, error) { return NewMCPLinkBackend(cfg, registry, tokens, nil, configs, users) },
		"server configuration": func() (*MCPLinkBackend, error) { return NewMCPLinkBackend(cfg, registry, tokens, states, nil, users) },
		"users":                func() (*MCPLinkBackend, error) { return NewMCPLinkBackend(cfg, registry, tokens, states, configs, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			if backend, err := construct(); err == nil || backend != nil {
				t.Fatal("missing authority or store was accepted")
			}
		})
	}
}

func TestMCPLinkBackendRequiresTrustedActiveIdentityAndBindsCallbackSession(t *testing.T) {
	fixture := newMCPLinkFixture(t)
	backend := &MCPLinkBackend{service: fixture.service, resolver: fixture.delegated.resolver}
	identity := MCPLinkIdentity{CanonicalUserID: fixture.canonical, EffectiveUserID: fixture.session.Subject, SessionID: fixture.session.ID}
	for _, invalid := range []MCPLinkIdentity{{}, {CanonicalUserID: identity.CanonicalUserID, SessionID: identity.SessionID}, {CanonicalUserID: identity.CanonicalUserID, EffectiveUserID: identity.EffectiveUserID}, {CanonicalUserID: "deleted", EffectiveUserID: identity.EffectiveUserID, SessionID: identity.SessionID}} {
		if _, err := backend.Status(context.Background(), invalid, testMCPServer); err == nil {
			t.Fatal("untrusted or inactive identity accepted")
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.Initiate(canceled, identity, testMCPServer, "/", ""); err == nil {
		t.Fatal("canceled identity context accepted")
	}
	initiation, err := backend.Initiate(context.Background(), identity, testMCPServer, "/", "")
	if err != nil {
		t.Fatal(err)
	}
	authURL, err := url.Parse(initiation.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	state := authURL.Query().Get("state")
	payload, err := fixture.service.keyring.decryptMCPAuthState(state)
	if err != nil {
		t.Fatal(err)
	}
	if payload.SessionIDHash != fixture.service.keyring.mcpSessionHash(identity.SessionID) || payload.CodeVerifier == "" || payload.Nonce == "" {
		t.Fatal("SDK initiation lost session HMAC, PKCE or nonce binding")
	}
	other := identity
	other.SessionID = "other-session"
	if _, err = backend.Complete(context.Background(), other, "good-code", state); err == nil {
		t.Fatal("cross-session SDK callback accepted")
	}
	result, err := backend.Complete(context.Background(), identity, "good-code", state)
	if err != nil {
		t.Fatal(err)
	}
	if result.ServerName != testMCPServer {
		t.Fatal("SDK completion selected a different server")
	}
	if _, err = backend.Complete(context.Background(), identity, "good-code", state); err == nil {
		t.Fatal("replayed SDK callback accepted")
	}
	status, err := backend.Status(context.Background(), identity, testMCPServer)
	if err != nil || !status.Connected {
		t.Fatal("SDK link was not persisted", err)
	}
}
