package account

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	accountdomain "github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/infra/persistence/relational"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

func TestListOperationRecordsRejectsCredentialRefreshOnWeb(t *testing.T) {
	service := NewService(nil, nil, nil, nil, nil, nil, nil)
	_, _, err := service.ListOperationRecords(context.Background(), OperationRecordQuery{
		Provider: accountdomain.ProviderWeb, OpType: accountdomain.OperationCredentialRefresh, Page: 1, PageSize: 20,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
}

func TestListOperationRecordsAttachesLatestAndFilters(t *testing.T) {
	ctx := context.Background()
	database, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "operation-records.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	accounts := relational.NewAccountRepository(database)
	logs := relational.NewAccountOperationLogRepository(database)
	service := NewService(accounts, nil, nil, nil, nil, nil, nil)
	service.SetOperationLogRepository(logs)
	service.SetLogger(slog.Default())

	successAccount, _, err := accounts.UpsertByIdentity(ctx, accountdomain.Credential{
		Provider: accountdomain.ProviderBuild, Name: "ok", SourceKey: "ok",
		EncryptedAccessToken: "a", EncryptedRefreshToken: "r", Enabled: true, AuthStatus: accountdomain.AuthStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	failedAccount, _, err := accounts.UpsertByIdentity(ctx, accountdomain.Credential{
		Provider: accountdomain.ProviderBuild, Name: "bad", SourceKey: "bad",
		EncryptedAccessToken: "a", EncryptedRefreshToken: "r", Enabled: true, AuthStatus: accountdomain.AuthStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	neverAccount, _, err := accounts.UpsertByIdentity(ctx, accountdomain.Credential{
		Provider: accountdomain.ProviderBuild, Name: "never", SourceKey: "never",
		EncryptedAccessToken: "a", EncryptedRefreshToken: "r", Enabled: true, AuthStatus: accountdomain.AuthStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 16, 0, 0, 0, time.UTC)
	if _, err := logs.Append(ctx, accountdomain.OperationLog{
		AccountID: successAccount.ID, Provider: accountdomain.ProviderBuild, OpType: accountdomain.OperationCredentialRefresh,
		Success: true, StatusCode: 200, TriggeredBy: accountdomain.OperationTriggerManual, StartedAt: now, FinishedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := logs.Append(ctx, accountdomain.OperationLog{
		AccountID: failedAccount.ID, Provider: accountdomain.ProviderBuild, OpType: accountdomain.OperationCredentialRefresh,
		Success: false, StatusCode: 400, ErrorCode: "invalid_grant", Message: "expired",
		TriggeredBy: accountdomain.OperationTriggerBatch, StartedAt: now.Add(time.Minute), FinishedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	all, total, err := service.ListOperationRecords(ctx, OperationRecordQuery{
		Provider: accountdomain.ProviderBuild, OpType: accountdomain.OperationCredentialRefresh, Page: 1, PageSize: 20,
	})
	if err != nil || total != 3 || len(all) != 3 {
		t.Fatalf("all = %d total=%d err=%v", len(all), total, err)
	}

	successItems, successTotal, err := service.ListOperationRecords(ctx, OperationRecordQuery{
		Provider: accountdomain.ProviderBuild, OpType: accountdomain.OperationCredentialRefresh, Result: "success", Page: 1, PageSize: 20,
	})
	if err != nil || successTotal != 1 || len(successItems) != 1 || successItems[0].Account.ID != successAccount.ID || successItems[0].Latest == nil || !successItems[0].Latest.Success {
		t.Fatalf("success filter = %#v total=%d err=%v", successItems, successTotal, err)
	}

	failedItems, failedTotal, err := service.ListOperationRecords(ctx, OperationRecordQuery{
		Provider: accountdomain.ProviderBuild, OpType: accountdomain.OperationCredentialRefresh, Result: "failed", Page: 1, PageSize: 20,
	})
	if err != nil || failedTotal != 1 || failedItems[0].Account.ID != failedAccount.ID || failedItems[0].Latest == nil || failedItems[0].Latest.Success {
		t.Fatalf("failed filter = %#v total=%d err=%v", failedItems, failedTotal, err)
	}

	neverItems, neverTotal, err := service.ListOperationRecords(ctx, OperationRecordQuery{
		Provider: accountdomain.ProviderBuild, OpType: accountdomain.OperationCredentialRefresh, Result: "never", Page: 1, PageSize: 20,
	})
	if err != nil || neverTotal != 1 || neverItems[0].Account.ID != neverAccount.ID || neverItems[0].Latest != nil {
		t.Fatalf("never filter = %#v total=%d err=%v", neverItems, neverTotal, err)
	}
}

func TestListOperationLogsReturnsHistoryAndNotFound(t *testing.T) {
	ctx := context.Background()
	database, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "operation-logs-list.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	accounts := relational.NewAccountRepository(database)
	logs := relational.NewAccountOperationLogRepository(database)
	service := NewService(accounts, nil, nil, nil, nil, nil, nil)
	service.SetOperationLogRepository(logs)

	account, _, err := accounts.UpsertByIdentity(ctx, accountdomain.Credential{
		Provider: accountdomain.ProviderWeb, Name: "hist", SourceKey: "hist",
		EncryptedAccessToken: "a", Enabled: true, AuthStatus: accountdomain.AuthStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 5, 17, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		finished := base.Add(time.Duration(i) * time.Minute)
		if _, err := logs.Append(ctx, accountdomain.OperationLog{
			AccountID: account.ID, Provider: accountdomain.ProviderWeb, OpType: accountdomain.OperationQuotaSync,
			Success: i%2 == 0, TriggeredBy: accountdomain.OperationTriggerManual, StartedAt: finished, FinishedAt: finished,
		}); err != nil {
			t.Fatal(err)
		}
	}
	history, err := service.ListOperationLogs(ctx, account.ID, accountdomain.OperationQuotaSync)
	if err != nil || len(history) != 3 || !history[0].FinishedAt.After(history[2].FinishedAt) {
		t.Fatalf("history = %#v err=%v", history, err)
	}
	if _, err := service.ListOperationLogs(ctx, 99999, accountdomain.OperationQuotaSync); !errors.Is(err, repository.ErrNotFound) {
		// mapRepositoryError may wrap
		if err == nil || !errors.Is(err, repository.ErrNotFound) && err.Error() == "" {
			t.Fatalf("missing account err = %v", err)
		}
	}
}
