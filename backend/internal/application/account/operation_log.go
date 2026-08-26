package account

import (
	"context"
	"errors"
	"strings"
	"time"

	accountdomain "github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

type operationTriggerContextKey struct{}

func withOperationTrigger(ctx context.Context, trigger accountdomain.OperationTrigger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !trigger.IsValid() {
		return ctx
	}
	return context.WithValue(ctx, operationTriggerContextKey{}, trigger)
}

func operationTriggerFrom(ctx context.Context) (accountdomain.OperationTrigger, bool) {
	if ctx == nil {
		return "", false
	}
	trigger, ok := ctx.Value(operationTriggerContextKey{}).(accountdomain.OperationTrigger)
	return trigger, ok && trigger.IsValid()
}

func resolveOperationTrigger(ctx context.Context, fallback accountdomain.OperationTrigger) accountdomain.OperationTrigger {
	if trigger, ok := operationTriggerFrom(ctx); ok {
		return trigger
	}
	if fallback.IsValid() {
		return fallback
	}
	return accountdomain.OperationTriggerManual
}

// SetOperationLogRepository 注入账号运维结果历史仓库；nil 时跳过写入。
func (s *Service) SetOperationLogRepository(value repository.AccountOperationLogRepository) {
	if s == nil {
		return
	}
	s.operationLogs = value
}

func (s *Service) recordOperationLog(ctx context.Context, value accountdomain.OperationLog) {
	if s == nil || s.operationLogs == nil {
		return
	}
	if value.AccountID == 0 || !value.OpType.IsValid() || !value.Provider.IsValid() {
		return
	}
	if !value.TriggeredBy.IsValid() {
		value.TriggeredBy = resolveOperationTrigger(ctx, accountdomain.OperationTriggerManual)
	}
	now := s.now().UTC()
	if value.FinishedAt.IsZero() {
		value.FinishedAt = now
	}
	if value.StartedAt.IsZero() {
		value.StartedAt = value.FinishedAt
	}
	value.RawResponse = accountdomain.TruncateOperationRawResponse(value.RawResponse)
	if value.Message == "" && !value.Success && value.ErrorCode == "skipped" {
		value.Message = "operation skipped"
	}
	persistCtx := ctx
	if persistCtx == nil {
		persistCtx = context.Background()
	}
	if _, err := s.operationLogs.Append(persistCtx, value); err != nil && s.logger != nil {
		s.logger.Error("append account operation log failed", "account_id", value.AccountID, "op_type", value.OpType, "error", err)
	}
}

func (s *Service) recordCredentialRefreshResult(ctx context.Context, credential accountdomain.Credential, trigger accountdomain.OperationTrigger, startedAt time.Time, refreshErr error) {
	if s == nil || s.operationLogs == nil || credential.ID == 0 {
		return
	}
	if !trigger.IsValid() {
		if value, ok := operationTriggerFrom(ctx); ok {
			trigger = value
		} else {
			return
		}
	}
	log := accountdomain.OperationLog{
		AccountID:   credential.ID,
		Provider:    credential.Provider,
		OpType:      accountdomain.OperationCredentialRefresh,
		TriggeredBy: trigger,
		StartedAt:   startedAt,
		FinishedAt:  s.now().UTC(),
		Success:     refreshErr == nil,
	}
	if refreshErr == nil {
		log.StatusCode = 200
		log.Message = "credential refreshed"
		s.recordOperationLog(ctx, log)
		return
	}
	if errors.Is(refreshErr, context.Canceled) {
		return
	}
	log.ErrorCode = "oauth_transport_error"
	log.Message = "OAuth request failed"
	var typed *provider.CredentialRefreshError
	if errors.As(refreshErr, &typed) {
		log.StatusCode = typed.Status
		log.ErrorCode = strings.TrimSpace(typed.Code)
		if log.ErrorCode == "" {
			log.ErrorCode = "oauth_refresh_error"
		}
		if message := normalizeCredentialRefreshErrorMessage(typed.Message); message != "" {
			log.Message = message
		}
		log.RawResponse = normalizeCredentialRefreshErrorResponse(typed.Response)
	} else if errors.Is(refreshErr, context.DeadlineExceeded) {
		log.ErrorCode = "oauth_timeout"
		log.Message = "OAuth request timed out"
	} else if message := strings.TrimSpace(refreshErr.Error()); message != "" {
		log.Message = normalizeCredentialRefreshErrorMessage(message)
	}
	s.recordOperationLog(ctx, log)
}

func (s *Service) recordQuotaSyncResult(ctx context.Context, credential accountdomain.Credential, startedAt time.Time, syncErr error) {
	if s == nil || s.operationLogs == nil || credential.ID == 0 {
		return
	}
	log := accountdomain.OperationLog{
		AccountID:   credential.ID,
		Provider:    credential.Provider,
		OpType:      accountdomain.OperationQuotaSync,
		TriggeredBy: resolveOperationTrigger(ctx, accountdomain.OperationTriggerManual),
		StartedAt:   startedAt,
		FinishedAt:  s.now().UTC(),
		Success:     syncErr == nil,
	}
	if syncErr == nil {
		log.StatusCode = 200
		log.Message = "quota synced"
		s.recordOperationLog(ctx, log)
		return
	}
	if errors.Is(syncErr, context.Canceled) {
		return
	}
	log.ErrorCode = "quota_sync_failed"
	log.Message = normalizeCredentialRefreshErrorMessage(syncErr.Error())
	if errors.Is(syncErr, provider.ErrUnauthorized) {
		log.StatusCode = 401
		log.ErrorCode = "unauthorized"
	}
	s.recordOperationLog(ctx, log)
}

func (s *Service) recordSkippedCredentialRefresh(ctx context.Context, credential accountdomain.Credential, reason string) {
	if s == nil || s.operationLogs == nil || credential.ID == 0 {
		return
	}
	now := s.now().UTC()
	s.recordOperationLog(ctx, accountdomain.OperationLog{
		AccountID:   credential.ID,
		Provider:    credential.Provider,
		OpType:      accountdomain.OperationCredentialRefresh,
		Success:     false,
		ErrorCode:   "skipped",
		Message:     reason,
		TriggeredBy: resolveOperationTrigger(ctx, accountdomain.OperationTriggerBatch),
		StartedAt:   now,
		FinishedAt:  now,
	})
}
