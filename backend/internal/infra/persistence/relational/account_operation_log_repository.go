package relational

import (
	"context"
	"fmt"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

// AccountOperationLogRepository 持久化账号额度同步与凭据刷新历史。
type AccountOperationLogRepository struct {
	db *Database
}

// NewAccountOperationLogRepository 创建操作日志仓库。
func NewAccountOperationLogRepository(db *Database) *AccountOperationLogRepository {
	return &AccountOperationLogRepository{db: db}
}

var _ repository.AccountOperationLogRepository = (*AccountOperationLogRepository)(nil)

// Append 写入一条操作日志，并 best-effort 将同账号同类型历史裁剪到上限。
func (r *AccountOperationLogRepository) Append(ctx context.Context, value account.OperationLog) (account.OperationLog, error) {
	if r == nil || r.db == nil {
		return account.OperationLog{}, fmt.Errorf("operation log repository is nil")
	}
	if value.AccountID == 0 {
		return account.OperationLog{}, fmt.Errorf("account id is required")
	}
	if !value.Provider.IsValid() {
		return account.OperationLog{}, fmt.Errorf("invalid provider %q", value.Provider)
	}
	if !value.OpType.IsValid() {
		return account.OperationLog{}, fmt.Errorf("invalid operation type %q", value.OpType)
	}
	if !value.TriggeredBy.IsValid() {
		return account.OperationLog{}, fmt.Errorf("invalid operation trigger %q", value.TriggeredBy)
	}

	now := time.Now().UTC()
	if value.FinishedAt.IsZero() {
		value.FinishedAt = now
	} else {
		value.FinishedAt = value.FinishedAt.UTC()
	}
	if value.StartedAt.IsZero() {
		value.StartedAt = value.FinishedAt
	} else {
		value.StartedAt = value.StartedAt.UTC()
	}
	value.RawResponse = account.TruncateOperationRawResponse(value.RawResponse)
	value.CreatedAt = now

	model := operationLogToModel(value)
	if err := r.db.db.WithContext(ctx).Create(&model).Error; err != nil {
		return account.OperationLog{}, fmt.Errorf("append account operation log: %w", err)
	}
	stored := operationLogFromModel(model)
	if err := r.Trim(ctx, stored.AccountID, stored.OpType, account.MaxOperationLogsPerType); err != nil {
		// Best-effort trim: primary write already succeeded.
		_ = err
	}
	return stored, nil
}

// ListByAccount 返回指定账号与操作类型的历史，按 finished_at desc, id desc。
func (r *AccountOperationLogRepository) ListByAccount(ctx context.Context, accountID uint64, opType account.OperationType, limit int) ([]account.OperationLog, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("operation log repository is nil")
	}
	if accountID == 0 {
		return []account.OperationLog{}, nil
	}
	if !opType.IsValid() {
		return nil, fmt.Errorf("invalid operation type %q", opType)
	}
	if limit <= 0 {
		limit = account.MaxOperationLogsPerType
	}

	var models []accountOperationLogModel
	err := r.db.db.WithContext(ctx).
		Where("account_id = ? AND op_type = ?", accountID, string(opType)).
		Order("finished_at DESC, id DESC").
		Limit(limit).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list account operation logs: %w", err)
	}
	result := make([]account.OperationLog, 0, len(models))
	for _, model := range models {
		result = append(result, operationLogFromModel(model))
	}
	return result, nil
}

// LatestByAccountIDs 返回各账号在指定操作类型下的最新一条记录。
func (r *AccountOperationLogRepository) LatestByAccountIDs(ctx context.Context, accountIDs []uint64, opType account.OperationType) (map[uint64]account.OperationLog, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("operation log repository is nil")
	}
	result := make(map[uint64]account.OperationLog, len(accountIDs))
	if len(accountIDs) == 0 {
		return result, nil
	}
	if !opType.IsValid() {
		return nil, fmt.Errorf("invalid operation type %q", opType)
	}

	var models []accountOperationLogModel
	err := r.db.db.WithContext(ctx).
		Where("account_id IN ? AND op_type = ?", accountIDs, string(opType)).
		Order("finished_at DESC, id DESC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("latest account operation logs: %w", err)
	}
	for _, model := range models {
		if _, exists := result[model.AccountID]; exists {
			continue
		}
		result[model.AccountID] = operationLogFromModel(model)
	}
	return result, nil
}

// Trim 将同账号同类型历史裁剪为最多 keep 条。
func (r *AccountOperationLogRepository) Trim(ctx context.Context, accountID uint64, opType account.OperationType, keep int) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("operation log repository is nil")
	}
	if accountID == 0 || !opType.IsValid() {
		return nil
	}
	if keep <= 0 {
		keep = account.MaxOperationLogsPerType
	}

	var keepIDs []uint64
	err := r.db.db.WithContext(ctx).
		Model(&accountOperationLogModel{}).
		Where("account_id = ? AND op_type = ?", accountID, string(opType)).
		Order("finished_at DESC, id DESC").
		Limit(keep).
		Pluck("id", &keepIDs).Error
	if err != nil {
		return fmt.Errorf("select account operation logs to keep: %w", err)
	}
	if len(keepIDs) == 0 {
		return nil
	}

	query := r.db.db.WithContext(ctx).
		Where("account_id = ? AND op_type = ?", accountID, string(opType))
	if len(keepIDs) > 0 {
		query = query.Where("id NOT IN ?", keepIDs)
	}
	if err := query.Delete(&accountOperationLogModel{}).Error; err != nil {
		return fmt.Errorf("trim account operation logs: %w", err)
	}
	return nil
}

func operationLogToModel(value account.OperationLog) accountOperationLogModel {
	return accountOperationLogModel{
		ID:          value.ID,
		AccountID:   value.AccountID,
		Provider:    string(value.Provider),
		OpType:      string(value.OpType),
		Success:     value.Success,
		StatusCode:  value.StatusCode,
		ErrorCode:   value.ErrorCode,
		Message:     value.Message,
		RawResponse: value.RawResponse,
		TriggeredBy: string(value.TriggeredBy),
		StartedAt:   value.StartedAt.UTC(),
		FinishedAt:  value.FinishedAt.UTC(),
		CreatedAt:   value.CreatedAt.UTC(),
	}
}

func operationLogFromModel(value accountOperationLogModel) account.OperationLog {
	return account.OperationLog{
		ID:          value.ID,
		AccountID:   value.AccountID,
		Provider:    account.Provider(value.Provider),
		OpType:      account.OperationType(value.OpType),
		Success:     value.Success,
		StatusCode:  value.StatusCode,
		ErrorCode:   value.ErrorCode,
		Message:     value.Message,
		RawResponse: value.RawResponse,
		TriggeredBy: account.OperationTrigger(value.TriggeredBy),
		StartedAt:   value.StartedAt.UTC(),
		FinishedAt:  value.FinishedAt.UTC(),
		CreatedAt:   value.CreatedAt.UTC(),
	}
}


