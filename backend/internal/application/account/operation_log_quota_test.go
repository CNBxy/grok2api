package account

import (
	"context"
	"encoding/base64"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	accountdomain "github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/infra/persistence/relational"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/infra/security"
)

func TestRefreshQuotaWritesQuotaOperationLog(t *testing.T) {
	ctx := context.Background()
	service, credential, logs, _ := newOperationLogQuotaService(t, accountdomain.ProviderWeb, nil)

	if _, err := service.RefreshQuota(ctx, credential.ID); err != nil {
		t.Fatal(err)
	}
	latest, err := logs.LatestByAccountIDs(ctx, []uint64{credential.ID}, accountdomain.OperationQuotaSync)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := latest[credential.ID]
	if !ok || !entry.Success || entry.TriggeredBy != accountdomain.OperationTriggerManual || entry.OpType != accountdomain.OperationQuotaSync {
		t.Fatalf("latest = %#v", entry)
	}
}

func TestBatchRefreshQuotaWritesBatchTrigger(t *testing.T) {
	ctx := context.Background()
	service, credential, logs, _ := newOperationLogQuotaService(t, accountdomain.ProviderConsole, nil)

	succeeded, failed, err := service.BatchRefreshQuota(ctx, []uint64{credential.ID})
	if err != nil || succeeded != 1 || failed != 0 {
		t.Fatalf("batch = %d/%d err=%v", succeeded, failed, err)
	}
	latest, err := logs.LatestByAccountIDs(ctx, []uint64{credential.ID}, accountdomain.OperationQuotaSync)
	if err != nil {
		t.Fatal(err)
	}
	entry := latest[credential.ID]
	if !entry.Success || entry.TriggeredBy != accountdomain.OperationTriggerBatch {
		t.Fatalf("batch log = %#v", entry)
	}
}

func TestRefreshBillingWritesQuotaOperationLog(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 5, 15, 0, 0, 0, time.UTC)
	service, credential, logs, adapter := newOperationLogCredentialService(t, now)
	adapter.billing = accountdomain.Billing{MonthlyLimit: 100, Used: 20, SyncedAt: now}

	if _, err := service.RefreshBilling(ctx, credential.ID); err != nil {
		t.Fatal(err)
	}
	latest, err := logs.LatestByAccountIDs(ctx, []uint64{credential.ID}, accountdomain.OperationQuotaSync)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := latest[credential.ID]
	if !ok || !entry.Success || entry.OpType != accountdomain.OperationQuotaSync {
		t.Fatalf("billing log = %#v", entry)
	}
}

func TestRefreshQuotaFailureWritesQuotaOperationLog(t *testing.T) {
	ctx := context.Background()
	service, credential, logs, _ := newOperationLogQuotaService(t, accountdomain.ProviderWeb, provider.ErrUnauthorized)

	if _, err := service.RefreshQuota(ctx, credential.ID); err == nil {
		t.Fatal("expected unauthorized")
	}
	latest, err := logs.LatestByAccountIDs(ctx, []uint64{credential.ID}, accountdomain.OperationQuotaSync)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := latest[credential.ID]
	if !ok || entry.Success || entry.ErrorCode != "unauthorized" || entry.StatusCode != 401 {
		t.Fatalf("failure log = %#v", entry)
	}
}

func newOperationLogQuotaService(t *testing.T, providerValue accountdomain.Provider, syncErr error) (*Service, accountdomain.Credential, *relational.AccountOperationLogRepository, *operationLogQuotaAdapter) {
	t.Helper()
	ctx := context.Background()
	database, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "operation-log-quota.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	accounts := relational.NewAccountRepository(database)
	logs := relational.NewAccountOperationLogRepository(database)
	cipher, err := security.NewCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.Encrypt("token")
	if err != nil {
		t.Fatal(err)
	}
	credential, _, err := accounts.UpsertByIdentity(ctx, accountdomain.Credential{
		Provider: providerValue, Name: "quota-op", SourceKey: "quota-op-" + string(providerValue),
		EncryptedAccessToken: token, Enabled: true, AuthStatus: accountdomain.AuthStatusActive, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &operationLogQuotaAdapter{providerValue: providerValue, err: syncErr}
	service := NewService(accounts, nil, nil, nil, provider.NewRegistry(adapter), cipher, nil)
	service.SetOperationLogRepository(logs)
	service.SetLogger(slog.Default())
	return service, credential, logs, adapter
}

type operationLogQuotaAdapter struct {
	providerValue accountdomain.Provider
	err           error
}

func (a *operationLogQuotaAdapter) Provider() accountdomain.Provider { return a.providerValue }

func (a *operationLogQuotaAdapter) Definition() provider.Definition {
	return provider.Definition{
		Provider: a.providerValue, ModelNamespace: a.providerValue.ModelNamespace(),
		Quota: provider.QuotaRemoteWindow, Credential: provider.CredentialSurface{AuthType: accountdomain.AuthTypeSSO},
	}
}

func (a *operationLogQuotaAdapter) SyncQuota(context.Context, accountdomain.Credential) (provider.QuotaSnapshot, error) {
	if a.err != nil {
		return provider.QuotaSnapshot{}, a.err
	}
	now := time.Now().UTC()
	return provider.QuotaSnapshot{
		Tier: accountdomain.WebTierSuper, SyncedAt: now,
		Windows: []accountdomain.QuotaWindow{{Mode: "default", Remaining: 5, Total: 10, SyncedAt: &now, UpdatedAt: now}},
	}, nil
}

func (a *operationLogQuotaAdapter) SyncQuotaMode(_ context.Context, _ accountdomain.Credential, mode string) (accountdomain.QuotaWindow, error) {
	return accountdomain.QuotaWindow{Mode: mode}, a.err
}
