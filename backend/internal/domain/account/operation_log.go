package account

import "time"

// OperationType 表示账号运维操作类型（额度同步 / 凭据刷新）。
type OperationType string

const (
	OperationQuotaSync         OperationType = "quota_sync"
	OperationCredentialRefresh OperationType = "credential_refresh"
)

// IsValid 判断操作类型是否受支持。
func (value OperationType) IsValid() bool {
	switch value {
	case OperationQuotaSync, OperationCredentialRefresh:
		return true
	default:
		return false
	}
}

// OperationTrigger 表示触发来源。
type OperationTrigger string

const (
	OperationTriggerManual    OperationTrigger = "manual"
	OperationTriggerBatch     OperationTrigger = "batch"
	OperationTriggerScheduler OperationTrigger = "scheduler"
)

// IsValid 判断触发来源是否受支持。
func (value OperationTrigger) IsValid() bool {
	switch value {
	case OperationTriggerManual, OperationTriggerBatch, OperationTriggerScheduler:
		return true
	default:
		return false
	}
}

// MaxOperationLogsPerType 限制每个账号在同一操作类型下保留的历史条数。
const MaxOperationLogsPerType = 20

// MaxOperationRawResponseBytes 限制 raw_response 落库长度，避免大包撑爆存储。
const MaxOperationRawResponseBytes = 4 << 10

// OperationLog 表示一次额度同步或凭据刷新的结果快照。
type OperationLog struct {
	ID          uint64
	AccountID   uint64
	Provider    Provider
	OpType      OperationType
	Success     bool
	StatusCode  int
	ErrorCode   string
	Message     string
	RawResponse string
	TriggeredBy OperationTrigger
	StartedAt   time.Time
	FinishedAt  time.Time
	CreatedAt   time.Time
}

// TruncateOperationRawResponse 将原始响应截断到落库上限。
func TruncateOperationRawResponse(value string) string {
	if len(value) <= MaxOperationRawResponseBytes {
		return value
	}
	return value[:MaxOperationRawResponseBytes]
}
