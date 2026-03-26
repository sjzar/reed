package reed

import (
	"context"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/security"
)

// SecretResolver wraps a RunResolver to inject secrets from a SecretStore.
type SecretResolver struct {
	next  RunResolver
	store *security.SecretStore
}

// NewSecretResolver creates a SecretResolver that wraps next.
func NewSecretResolver(next RunResolver, store *security.SecretStore) *SecretResolver {
	return &SecretResolver{next: next, store: store}
}

// ResolveRunRequest delegates to next and injects secrets into the result.
func (sr *SecretResolver) ResolveRunRequest(ctx context.Context, wf *model.Workflow, wfSource string, params model.TriggerParams) (*model.RunRequest, error) {
	req, err := sr.next.ResolveRunRequest(ctx, wf, wfSource, params)
	if err != nil {
		return nil, err
	}
	req.Secrets = sr.store.Snapshot()
	return req, nil
}

// BuildResolver composes the standard resolver chain.
// base handles workflow resolution; store (if non-nil) adds secret injection.
func BuildResolver(base RunResolver, store *security.SecretStore) RunResolver {
	r := base
	if store != nil {
		r = NewSecretResolver(r, store)
	}
	return r
}
