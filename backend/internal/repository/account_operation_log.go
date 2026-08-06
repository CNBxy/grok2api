package repository

import (
	"context"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
)

// AccountOperationLogRepository 持久化账号额度同步与凭据刷新结果历史。
type AccountOperationLogRepository interface {
	// Append 写入一条操作日志，并 best-effort 将同账号同类型历史裁剪到 MaxOperationLogsPerType。
	Append(ctx context.Context, value account.OperationLog) (account.OperationLog, error)
	// ListByAccount 按 finished_at desc, id desc 返回指定账号与操作类型的历史（最多 limit 条）。
	ListByAccount(ctx context.Context, accountID uint64, opType account.OperationType, limit int) ([]account.OperationLog, error)
	// LatestByAccountIDs 返回各账号在指定操作类型下的最新一条；无记录的 id 不会出现在 map 中。
	LatestByAccountIDs(ctx context.Context, accountIDs []uint64, opType account.OperationType) (map[uint64]account.OperationLog, error)
	// Trim 将同账号同类型历史裁剪为最多 keep 条（按 finished_at desc, id desc）。
	Trim(ctx context.Context, accountID uint64, opType account.OperationType, keep int) error
}
