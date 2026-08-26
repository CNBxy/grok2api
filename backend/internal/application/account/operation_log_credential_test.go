package account

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	accountdomain "github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/infra/persistence/relational"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/infra/runtime/memory"
)

func TestRefreshTokenWritesCredentialOperationLog(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	service, credential, logs, adapter := newOperationLogCredentialService(t, now)

	if _, err := service.ensureCredential(ctx, credential, ensureCredentialOptions{
		force: true, bypassCooldown: true, retryPermanentOnce: true, trigger: accountdomain.OperationTriggerManual,
	}); err != nil {
		t.Fatal(err)
	}
	if adapter.refreshCount.Load() != 1 {
		t.Fatalf("refresh count = %d", adapter.refreshCount.Load())
	}
	latest, err := logs.LatestByAccountIDs(ctx, []uint64{credential.ID}, accountdomain.OperationCredentialRefresh)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := latest[credential.ID]
	if !ok || !entry.Success || entry.TriggeredBy != accountdomain.OperationTriggerManual || entry.StatusCode != 200 {
		t.Fatalf("latest log = %#v", entry)
	}
}

func TestRefreshTokenFailureWritesCredentialOperationLog(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	service, credential, logs, adapter := newOperationLogCredentialService(t, now)
	adapter.refreshErr = &provider.CredentialRefreshError{Status: 400, Code: "invalid_grant", Message: "Refresh token has expired", Permanent: true}

	if _, err := service.ensureCredential(ctx, credential, ensureCredentialOptions{
		force: true, bypassCooldown: true, retryPermanentOnce: true, trigger: accountdomain.OperationTriggerManual,
	}); err == nil {
		t.Fatal("expected refresh failure")
	}
	latest, err := logs.LatestByAccountIDs(ctx, []uint64{credential.ID}, accountdomain.OperationCredentialRefresh)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := latest[credential.ID]
	if !ok || entry.Success || entry.ErrorCode != "invalid_grant" || entry.StatusCode != 400 || entry.Message == "" {
		t.Fatalf("failure log = %#v", entry)
	}
	if entry.TriggeredBy != accountdomain.OperationTriggerManual {
		t.Fatalf("trigger = %s", entry.TriggeredBy)
	}
}

func TestBatchRefreshTokensWritesSkippedOperationLog(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	service, _, logs, _ := newOperationLogCredentialService(t, now)

	skipped, _, err := service.accounts.UpsertByIdentity(ctx, accountdomain.Credential{
		Provider: accountdomain.ProviderBuild, Name: "no-refresh", SourceKey: "no-refresh",
		EncryptedAccessToken: "access-only", Enabled: true, AuthStatus: accountdomain.AuthStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}

	succeeded, failed, skippedCount, err := service.BatchRefreshTokens(ctx, []uint64{skipped.ID})
	if err != nil {
		t.Fatal(err)
	}
	if succeeded != 0 || failed != 0 || skippedCount != 1 {
		t.Fatalf("batch = %d/%d/%d", succeeded, failed, skippedCount)
	}
	latest, err := logs.LatestByAccountIDs(ctx, []uint64{skipped.ID}, accountdomain.OperationCredentialRefresh)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := latest[skipped.ID]
	if !ok || entry.Success || entry.ErrorCode != "skipped" || entry.TriggeredBy != accountdomain.OperationTriggerBatch {
		t.Fatalf("skipped log = %#v", entry)
	}
}

func TestBatchRefreshTokensWritesBatchTrigger(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	service, credential, logs, _ := newOperationLogCredentialService(t, now)

	succeeded, failed, skippedCount, err := service.BatchRefreshTokens(ctx, []uint64{credential.ID})
	if err != nil || succeeded != 1 || failed != 0 || skippedCount != 0 {
		t.Fatalf("batch = %d/%d/%d err=%v", succeeded, failed, skippedCount, err)
	}
	latest, err := logs.LatestByAccountIDs(ctx, []uint64{credential.ID}, accountdomain.OperationCredentialRefresh)
	if err != nil {
		t.Fatal(err)
	}
	entry := latest[credential.ID]
	if !entry.Success || entry.TriggeredBy != accountdomain.OperationTriggerBatch {
		t.Fatalf("batch log = %#v", entry)
	}
}

func TestEnsureCredentialWithoutTriggerDoesNotWriteOperationLog(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	service, credential, logs, _ := newOperationLogCredentialService(t, now)

	if _, err := service.EnsureCredential(ctx, credential, true); err != nil {
		t.Fatal(err)
	}
	latest, err := logs.LatestByAccountIDs(ctx, []uint64{credential.ID}, accountdomain.OperationCredentialRefresh)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 0 {
		t.Fatalf("gateway ensure must not write log, got %#v", latest)
	}
}

func TestSchedulerCredentialRefreshWritesSchedulerTrigger(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	service, credential, logs, _ := newOperationLogCredentialService(t, now)
	due := now.Add(-time.Minute)
	credential.RefreshDueAt = &due
	if _, err := service.accounts.Update(ctx, credential); err != nil {
		// Update may not set RefreshDueAt on routing row; force via UpdateTokens path then failure schedule.
		t.Logf("update credential schedule via ensure with respectSchedule")
	}
	// Direct scheduled ensure path used by RunCredentialRefresh.
	if _, err := service.ensureCredential(ctx, credential, ensureCredentialOptions{
		force: true, respectSchedule: true, trigger: accountdomain.OperationTriggerScheduler,
	}); err != nil {
		t.Fatal(err)
	}
	latest, err := logs.LatestByAccountIDs(ctx, []uint64{credential.ID}, accountdomain.OperationCredentialRefresh)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := latest[credential.ID]
	if !ok || !entry.Success || entry.TriggeredBy != accountdomain.OperationTriggerScheduler {
		t.Fatalf("scheduler log = %#v", entry)
	}
}

func newOperationLogCredentialService(t *testing.T, now time.Time) (*Service, accountdomain.Credential, *relational.AccountOperationLogRepository, *credentialRefreshAdapter) {
	t.Helper()
	ctx := context.Background()
	database, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "operation-log-credential.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	accounts := relational.NewAccountRepository(database)
	logs := relational.NewAccountOperationLogRepository(database)
	credential, _, err := accounts.UpsertByIdentity(ctx, accountdomain.Credential{
		Provider: accountdomain.ProviderBuild, Name: "op-log", SourceKey: "op-log",
		EncryptedAccessToken: "access-0", EncryptedRefreshToken: "refresh-0", ExpiresAt: now.Add(time.Hour),
		Enabled: true, AuthStatus: accountdomain.AuthStatusActive, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &credentialRefreshAdapter{}
	service := NewService(accounts, nil, nil, nil, provider.NewRegistry(adapter), nil, memory.NewLockStore())
	service.SetOperationLogRepository(logs)
	service.SetLogger(slog.Default())
	service.now = func() time.Time { return now }
	return service, credential, logs, adapter
}
