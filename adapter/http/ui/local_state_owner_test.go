package ui

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"github.com/viant/agently-core/service/auth"
	"github.com/viant/forge/backend/types"
	"github.com/viant/forge/backend/uicontext"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type ownerHTTPFixture struct {
	*selectionHTTPFixture
	calls   int
	resolve uicontext.LocalStateOwnerResolver
}

func (f *ownerHTTPFixture) AttachLocalStateOwner(ctx context.Context, w *types.Window) (*types.Window, error) {
	f.calls++
	if w.LocalStateOwner != nil {
		panic("authored owner reached trusted callback")
	}
	return uicontext.AttachLocalStateOwner(ctx, w, f.resolve)
}
func TestLocalStateOwnerHTTPAttachesOnlyAfterNormalSuccessfulProjection(t *testing.T) {
	f := &ownerHTTPFixture{selectionHTTPFixture: selectionHTTP(t)}
	f.window.LocalStateOwner = &types.LocalStateOwner{Binding: "AUTHORED", ValidUntil: time.Now().Add(time.Hour)}
	handler := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, WithWindowResourceProvider(f))
	get := func() *httptest.ResponseRecorder {
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/window/view?applyPermission=true", nil))
		return out
	}
	out := get()
	var data struct{ Data types.Window }
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &data) != nil || data.Data.LocalStateOwner != nil {
		t.Fatal("optional callback denied public metadata or retained authored owner")
	}
	f.resolve = func(context.Context) (*types.LocalStateOwner, error) {
		return &types.LocalStateOwner{Binding: "VERIFIED", ValidUntil: time.Now().Add(time.Minute)}, nil
	}
	out = get()
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &data) != nil || data.Data.LocalStateOwner == nil || data.Data.LocalStateOwner.Binding != "VERIFIED" {
		t.Fatal("verified owner not attached")
	}
	if f.gets != 2 || f.admits != 2 || f.calls != 2 {
		t.Fatal("extra read or skipped projection")
	}
	f.failAdmit = f.admits + 1
	out = get()
	if out.Code == 200 || f.calls != 2 {
		t.Fatal("failed admission attached owner")
	}
}

func TestLocalStateOwnerNormalHTTPVerifiedMiddleware(t *testing.T) {
	root := t.TempDir()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(root, "public.pem")
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}), 0600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("auth:\n  enabled: true\n  ipHashKey: test-only\n  jwt: {enabled: true, rsa: [%q]}\n", keyPath)
	if err = os.WriteFile(filepath.Join(root, "config.yaml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	runtime, err := auth.NewRuntime(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &ownerHTTPFixture{selectionHTTPFixture: selectionHTTP(t), resolve: auth.LocalStateOwner}
	f.window.Resource.AuthorityBinding = "public-local-definition"
	f.window.Resource.ValidUntil = time.Time{}
	f.window.LocalStateOwner = &types.LocalStateOwner{Binding: "AUTHORED", ValidUntil: time.Now().Add(time.Hour)}
	raw := NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, WithWindowResourceProvider(f))
	handler := auth.WithAuthProtection(raw, runtime)
	token := func(subject, account string) string {
		signed, e := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"sub": subject, "iss": "verified-test", "account_id": map[string]int{"account-a": 1, "account-b": 2}[account], "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}).SignedString(private)
		if e != nil {
			t.Fatal(e)
		}
		return signed
	}
	get := func(bearer string) (int, *types.LocalStateOwner) {
		request := httptest.NewRequest(http.MethodGet, "/window/view?applyPermission=true", nil)
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, request)
		var result struct{ Data types.Window }
		if out.Code == 200 {
			if e := json.Unmarshal(out.Body.Bytes(), &result); e != nil {
				t.Fatal(e)
			}
		}
		return out.Code, result.Data.LocalStateOwner
	}
	first := token("alice", "account-a")
	status, owner := get(first)
	if status != 200 || owner == nil || !owner.ValidUntil.After(time.Now()) {
		t.Fatal("normal verified metadata route did not attach owner", status)
	}
	if f.gets != 1 || f.admits != 1 || f.calls != 1 {
		t.Fatal("normal route added reads or skipped projection")
	}
	for _, credentials := range []string{token("bob", "account-a"), token("alice", "account-b")} {
		status, next := get(credentials)
		if status != 200 || next == nil || next.Binding == owner.Binding {
			t.Fatal("verified subject/account credential change retained owner")
		}
	}
	status, _ = get("")
	if status != 401 {
		t.Fatal("logout did not preserve normal metadata authentication")
	}
	if f.calls != 3 {
		t.Fatal("anonymous request reached attachment")
	}
	// The ordinary metadata handler without a configured verified resolver remains
	// available under the same transport policy, with no owner capability.
	f.resolve = nil
	status, next := get(first)
	if status != 200 || next != nil {
		t.Fatal("missing callback denied metadata or fabricated owner")
	}
}
