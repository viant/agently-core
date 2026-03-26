package dbconfig

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/viant/scy"
	"github.com/viant/scy/cred"
	_ "github.com/viant/scy/kms/blowfish"
)

func TestExpandDSN_NoSecretsRef(t *testing.T) {
	ctx := context.Background()
	dsn := "root:dev@tcp(127.0.0.1:3307)/agently?parseTime=true"

	expanded, resource, err := ExpandDSN(ctx, dsn, "")
	if err != nil {
		t.Fatalf("ExpandDSN() error = %v", err)
	}
	if expanded != dsn {
		t.Fatalf("ExpandDSN() expanded = %q, want %q", expanded, dsn)
	}
	if resource != nil {
		t.Fatalf("ExpandDSN() resource = %v, want nil", resource)
	}
}

func TestExpandDSN_PlainBasicSecretFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "mysql.json")
	if err := os.WriteFile(secretFile, []byte(`{"Username":"root","Password":"dev"}`), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	dsn := "${Username}:${Password}@tcp(127.0.0.1:3307)/agently?parseTime=true"
	want := "root:dev@tcp(127.0.0.1:3307)/agently?parseTime=true"

	expanded, resource, err := ExpandDSN(ctx, dsn, secretFile)
	if err != nil {
		t.Fatalf("ExpandDSN() error = %v", err)
	}
	if expanded != want {
		t.Fatalf("ExpandDSN() expanded = %q, want %q", expanded, want)
	}
	if resource == nil {
		t.Fatalf("ExpandDSN() resource was nil")
	}
}

func TestExpandDSN_EncodedResourceWithKey(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "mysql.enc.json")

	resource := scy.NewResource(&cred.Basic{}, secretFile, "blowfish://default")
	secret := scy.NewSecret(&cred.Basic{
		Username: "root",
		Password: "dev",
	}, resource)
	if err := scy.New().Store(ctx, secret); err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	dsn := "${Username}:${Password}@tcp(127.0.0.1:3307)/agently?parseTime=true"
	want := "root:dev@tcp(127.0.0.1:3307)/agently?parseTime=true"

	expanded, loadedResource, err := ExpandDSN(ctx, dsn, secretFile+"|blowfish://default")
	if err != nil {
		t.Fatalf("ExpandDSN() error = %v", err)
	}
	if expanded != want {
		t.Fatalf("ExpandDSN() expanded = %q, want %q", expanded, want)
	}
	if loadedResource == nil {
		t.Fatalf("ExpandDSN() resource was nil")
	}
	if loadedResource.Key != "blowfish://default" {
		t.Fatalf("ExpandDSN() resource.Key = %q, want %q", loadedResource.Key, "blowfish://default")
	}
}
