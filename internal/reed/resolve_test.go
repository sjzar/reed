package reed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/security"
)

type stubResolver struct {
	req *model.RunRequest
	err error
}

func (s *stubResolver) ResolveRunRequest(_ context.Context, _ *model.Workflow, _ string, _ model.TriggerParams) (*model.RunRequest, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := *s.req
	return &out, nil
}

func TestSecretResolver(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(p, []byte("API_KEY=secret123\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := security.NewSecretStore([]security.SecretSource{
		{Type: security.SecretSourceTypeFile, Path: p},
	})
	if err != nil {
		t.Fatal(err)
	}

	base := &stubResolver{req: &model.RunRequest{}}
	sr := NewSecretResolver(base, store)

	req, err := sr.ResolveRunRequest(context.Background(), nil, "", model.TriggerParams{})
	if err != nil {
		t.Fatal(err)
	}
	if req.Secrets["API_KEY"] != "secret123" {
		t.Errorf("Secrets = %v", req.Secrets)
	}
}

func TestSecretResolver_ErrorPassthrough(t *testing.T) {
	wantErr := fmt.Errorf("boom")
	base := &stubResolver{err: wantErr}

	sr := NewSecretResolver(base, nil)
	_, err := sr.ResolveRunRequest(context.Background(), nil, "", model.TriggerParams{})
	if err != wantErr {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

func TestBuildResolver_NilStore(t *testing.T) {
	base := &stubResolver{req: &model.RunRequest{}}
	r := BuildResolver(base, nil)
	if r != base {
		t.Error("expected BuildResolver to return base when store is nil")
	}
}

func TestBuildResolver_WithStore(t *testing.T) {
	store, err := security.NewSecretStore(nil)
	if err != nil {
		t.Fatal(err)
	}

	base := &stubResolver{req: &model.RunRequest{}}
	r := BuildResolver(base, store)
	if _, ok := r.(*SecretResolver); !ok {
		t.Errorf("expected *SecretResolver, got %T", r)
	}
}
