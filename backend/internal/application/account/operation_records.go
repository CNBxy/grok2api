package account

import (
	"context"
	"fmt"
	"sort"
	"strings"

	accountdomain "github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

// OperationRecord 是账号刷新记录列表中的一行：账号 + 指定 opType 的最新结果。
type OperationRecord struct {
	Account accountdomain.Credential
	Latest  *accountdomain.OperationLog
}

// OperationRecordQuery 定义操作记录列表查询。
type OperationRecordQuery struct {
	Provider accountdomain.Provider
	OpType   accountdomain.OperationType
	// Result: "" | success | failed | never
	Result   string
	Page     int
	PageSize int
	Search   string
	Status   string
	Sort     repository.SortQuery
}

// ListOperationRecords 返回当前小分类下的账号及最新操作结果。
func (s *Service) ListOperationRecords(ctx context.Context, query OperationRecordQuery) ([]OperationRecord, int64, error) {
	if err := validateOperationRecordQuery(query); err != nil {
		return nil, 0, err
	}
	page, pageSize := normalizePage(query.Page, query.PageSize)
	baseFilter := ListFilter{
		Provider: string(query.Provider),
		Status:   query.Status,
		Sort:     query.Sort,
	}
	if baseFilter.Sort.Field == "" {
		baseFilter.Sort = repository.SortQuery{Field: "createdAt", Direction: repository.SortDescending}
	}
	if !oneOf(baseFilter.Status, "", "active", "disabled", "reauthRequired", "cooldown", "waitingReset", "probing") ||
		!repository.IsValidSort(baseFilter.Sort, "name", "type", "status", "createdAt") {
		return nil, 0, ErrInvalidFilter
	}

	result := strings.TrimSpace(query.Result)
	if result == "" {
		return s.listOperationRecordsPage(ctx, query.Provider, query.OpType, page, pageSize, query.Search, baseFilter)
	}
	if !oneOf(result, "success", "failed", "never") {
		return nil, 0, ErrInvalidFilter
	}
	return s.listOperationRecordsFiltered(ctx, query.Provider, query.OpType, result, page, pageSize, query.Search, baseFilter)
}

// ListOperationLogs 返回指定账号某操作类型的历史（最多 MaxOperationLogsPerType）。
func (s *Service) ListOperationLogs(ctx context.Context, accountID uint64, opType accountdomain.OperationType) ([]accountdomain.OperationLog, error) {
	if accountID == 0 {
		return nil, fmt.Errorf("%w: 账号 ID 无效", ErrInvalidInput)
	}
	if !opType.IsValid() {
		return nil, fmt.Errorf("%w: 操作类型无效", ErrInvalidInput)
	}
	if _, err := s.accounts.Get(ctx, accountID); err != nil {
		return nil, mapRepositoryError(err)
	}
	if s.operationLogs == nil {
		return []accountdomain.OperationLog{}, nil
	}
	return s.operationLogs.ListByAccount(ctx, accountID, opType, accountdomain.MaxOperationLogsPerType)
}

func validateOperationRecordQuery(query OperationRecordQuery) error {
	if !query.Provider.IsValid() {
		return fmt.Errorf("%w: 账号来源无效", ErrInvalidInput)
	}
	if !query.OpType.IsValid() {
		return fmt.Errorf("%w: 操作类型无效", ErrInvalidInput)
	}
	if query.OpType == accountdomain.OperationCredentialRefresh && query.Provider != accountdomain.ProviderBuild {
		return fmt.Errorf("%w: 凭据刷新仅支持 Grok Build", ErrInvalidInput)
	}
	return nil
}

func (s *Service) listOperationRecordsPage(ctx context.Context, providerValue accountdomain.Provider, opType accountdomain.OperationType, page, pageSize int, search string, filter ListFilter) ([]OperationRecord, int64, error) {
	values, total, err := s.accounts.List(ctx, repository.AccountListQuery{
		Page: repository.PageQuery{Offset: (page - 1) * pageSize, Limit: pageSize, Search: search, Sort: filter.Sort},
		Filter: repository.AccountListFilter{
			Provider: string(providerValue), Status: filter.Status, Now: s.now(),
		},
	})
	if err != nil {
		return nil, 0, err
	}
	records, err := s.attachLatestOperations(ctx, values, opType)
	if err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

func (s *Service) listOperationRecordsFiltered(ctx context.Context, providerValue accountdomain.Provider, opType accountdomain.OperationType, result string, page, pageSize int, search string, filter ListFilter) ([]OperationRecord, int64, error) {
	const scanPageSize = 200
	all := make([]OperationRecord, 0)
	for scanPage := 1; ; scanPage++ {
		values, total, err := s.accounts.List(ctx, repository.AccountListQuery{
			Page: repository.PageQuery{Offset: (scanPage - 1) * scanPageSize, Limit: scanPageSize, Search: search, Sort: filter.Sort},
			Filter: repository.AccountListFilter{
				Provider: string(providerValue), Status: filter.Status, Now: s.now(),
			},
		})
		if err != nil {
			return nil, 0, err
		}
		batch, err := s.attachLatestOperations(ctx, values, opType)
		if err != nil {
			return nil, 0, err
		}
		for _, item := range batch {
			if matchesOperationResult(item.Latest, result) {
				all = append(all, item)
			}
		}
		if int64(scanPage*scanPageSize) >= total || len(values) == 0 {
			break
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		left, right := all[i].Latest, all[j].Latest
		switch {
		case left == nil && right == nil:
			return all[i].Account.ID > all[j].Account.ID
		case left == nil:
			return false
		case right == nil:
			return true
		case !left.FinishedAt.Equal(right.FinishedAt):
			return left.FinishedAt.After(right.FinishedAt)
		default:
			return left.ID > right.ID
		}
	})
	total := int64(len(all))
	start := (page - 1) * pageSize
	if start >= len(all) {
		return []OperationRecord{}, total, nil
	}
	end := min(start+pageSize, len(all))
	return all[start:end], total, nil
}

func (s *Service) attachLatestOperations(ctx context.Context, values []accountdomain.Credential, opType accountdomain.OperationType) ([]OperationRecord, error) {
	records := make([]OperationRecord, 0, len(values))
	if len(values) == 0 {
		return records, nil
	}
	ids := make([]uint64, 0, len(values))
	for _, value := range values {
		ids = append(ids, value.ID)
	}
	latest := map[uint64]accountdomain.OperationLog{}
	if s.operationLogs != nil {
		var err error
		latest, err = s.operationLogs.LatestByAccountIDs(ctx, ids, opType)
		if err != nil {
			return nil, err
		}
	}
	for _, value := range values {
		record := OperationRecord{Account: value}
		if log, ok := latest[value.ID]; ok {
			copy := log
			record.Latest = &copy
		}
		records = append(records, record)
	}
	return records, nil
}

func matchesOperationResult(latest *accountdomain.OperationLog, result string) bool {
	switch result {
	case "success":
		return latest != nil && latest.Success
	case "failed":
		return latest != nil && !latest.Success
	case "never":
		return latest == nil
	default:
		return true
	}
}
