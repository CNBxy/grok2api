package relational

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
)

func TestAccountOperationLogAppendListAndLatest(t *testing.T) {
	database := openTestDatabase(t)
	accounts := NewAccountRepository(database)
	logs := NewAccountOperationLogRepository(database)
	ctx := context.Background()

	first, _, err := accounts.UpsertByIdentity(ctx, account.Credential{
		Provider: account.ProviderBuild, Name: "one", SourceKey: "one",
		EncryptedAccessToken: testEncryptedToken, Enabled: true, AuthStatus: account.AuthStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := accounts.UpsertByIdentity(ctx, account.Credential{
		Provider: account.ProviderBuild, Name: "two", SourceKey: "two",
		EncryptedAccessToken: testEncryptedToken, Enabled: true, AuthStatus: account.AuthStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	older, err := logs.Append(ctx, account.OperationLog{
		AccountID: first.ID, Provider: account.ProviderBuild, OpType: account.OperationCredentialRefresh,
		Success: false, StatusCode: 500, ErrorCode: "upstream", Message: "temp fail",
		TriggeredBy: account.OperationTriggerManual, StartedAt: base, FinishedAt: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	newer, err := logs.Append(ctx, account.OperationLog{
		AccountID: first.ID, Provider: account.ProviderBuild, OpType: account.OperationCredentialRefresh,
		Success: true, StatusCode: 200, Message: "ok",
		TriggeredBy: account.OperationTriggerBatch, StartedAt: base.Add(time.Minute), FinishedAt: base.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logs.Append(ctx, account.OperationLog{
		AccountID: second.ID, Provider: account.ProviderBuild, OpType: account.OperationCredentialRefresh,
		Success: false, StatusCode: 0, ErrorCode: "skipped", Message: "no refresh token",
		TriggeredBy: account.OperationTriggerBatch, StartedAt: base.Add(2 * time.Minute), FinishedAt: base.Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	listed, err := logs.ListByAccount(ctx, first.ID, account.OperationCredentialRefresh, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("list len = %d, want 2", len(listed))
	}
	if listed[0].ID != newer.ID || listed[1].ID != older.ID {
		t.Fatalf("list order = %#v, want newest first", listed)
	}

	latest, err := logs.LatestByAccountIDs(ctx, []uint64{first.ID, second.ID, 999}, account.OperationCredentialRefresh)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 {
		t.Fatalf("latest map size = %d, want 2", len(latest))
	}
	if got := latest[first.ID]; !got.Success || got.ID != newer.ID {
		t.Fatalf("first latest = %#v", got)
	}
	if got := latest[second.ID]; got.Success || got.ErrorCode != "skipped" {
		t.Fatalf("second latest = %#v", got)
	}
	if _, ok := latest[999]; ok {
		t.Fatal("missing account must not appear in latest map")
	}
}

func TestAccountOperationLogAppendTrimsToMaxPerType(t *testing.T) {
	database := openTestDatabase(t)
	accounts := NewAccountRepository(database)
	logs := NewAccountOperationLogRepository(database)
	ctx := context.Background()

	value, _, err := accounts.UpsertByIdentity(ctx, account.Credential{
		Provider: account.ProviderWeb, Name: "quota", SourceKey: "quota",
		EncryptedAccessToken: testEncryptedToken, Enabled: true, AuthStatus: account.AuthStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < account.MaxOperationLogsPerType+3; i++ {
		finished := base.Add(time.Duration(i) * time.Minute)
		if _, err := logs.Append(ctx, account.OperationLog{
			AccountID: value.ID, Provider: account.ProviderWeb, OpType: account.OperationQuotaSync,
			Success: i%2 == 0, StatusCode: 200, Message: "n",
			TriggeredBy: account.OperationTriggerScheduler, StartedAt: finished, FinishedAt: finished,
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	listed, err := logs.ListByAccount(ctx, value.ID, account.OperationQuotaSync, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != account.MaxOperationLogsPerType {
		t.Fatalf("after append trim len = %d, want %d", len(listed), account.MaxOperationLogsPerType)
	}
	// Newest finished_at must remain.
	wantNewest := base.Add(time.Duration(account.MaxOperationLogsPerType+2) * time.Minute)
	if !listed[0].FinishedAt.Equal(wantNewest) {
		t.Fatalf("newest finished_at = %s, want %s", listed[0].FinishedAt, wantNewest)
	}
}

func TestAccountOperationLogAppendTruncatesRawResponse(t *testing.T) {
	database := openTestDatabase(t)
	accounts := NewAccountRepository(database)
	logs := NewAccountOperationLogRepository(database)
	ctx := context.Background()

	value, _, err := accounts.UpsertByIdentity(ctx, account.Credential{
		Provider: account.ProviderBuild, Name: "raw", SourceKey: "raw",
		EncryptedAccessToken: testEncryptedToken, Enabled: true, AuthStatus: account.AuthStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stored, err := logs.Append(ctx, account.OperationLog{
		AccountID: value.ID, Provider: account.ProviderBuild, OpType: account.OperationCredentialRefresh,
		Success: false, RawResponse: strings.Repeat("x", account.MaxOperationRawResponseBytes+64),
		TriggeredBy: account.OperationTriggerManual, StartedAt: now, FinishedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.RawResponse) != account.MaxOperationRawResponseBytes {
		t.Fatalf("raw len = %d, want %d", len(stored.RawResponse), account.MaxOperationRawResponseBytes)
	}
}

func TestAccountOperationLogCascadeDeleteWithAccount(t *testing.T) {
	database := openTestDatabase(t)
	accounts := NewAccountRepository(database)
	logs := NewAccountOperationLogRepository(database)
	ctx := context.Background()

	value, _, err := accounts.UpsertByIdentity(ctx, account.Credential{
		Provider: account.ProviderBuild, Name: "gone", SourceKey: "gone",
		EncryptedAccessToken: testEncryptedToken, Enabled: true, AuthStatus: account.AuthStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := logs.Append(ctx, account.OperationLog{
		AccountID: value.ID, Provider: account.ProviderBuild, OpType: account.OperationCredentialRefresh,
		Success: true, TriggeredBy: account.OperationTriggerManual, StartedAt: now, FinishedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.Delete(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	listed, err := logs.ListByAccount(ctx, value.ID, account.OperationCredentialRefresh, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("logs after account delete = %#v, want empty", listed)
	}
}
