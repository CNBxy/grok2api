# 账号刷新记录 Implementation Plan

> **For implementer:** 全程 TDD。先写失败测试 → 看它红 → 最小实现 → 看它绿 → 再提交。  
> **Design SoT:** `docs/plans/2026-08-05-account-refresh-records-design.md`（已冻结）

**Goal:** 管理端新增「账号刷新记录」页与后端操作日志：额度同步 / 凭据刷新按分类展示全部账号的最新结果与 ≤20 条历史，并支持精简批量运维。

**Architecture:** 新表 `account_operation_logs` + repository；在现有 `RefreshToken` / `RefreshQuota` / batch / scheduler 出口 best-effort Append+Trim；新读 API `operation-records` / `operation-logs`；前端独立 feature 页复用现有 batch 写接口。

**Tech Stack:** Go + GORM (SQLite/Postgres)、Gin admin API、React + Vite + TanStack Query + i18n（现有前端栈）

**Test commands (host may use Docker golang):**
```bash
cd /root/Grok/grok2api/backend
GOCACHE=/root/Grok/grok2api/.gocache go test ./internal/infra/persistence/relational/ -count=1 -run 'OperationLog' -v
GOCACHE=/root/Grok/grok2api/.gocache go test ./internal/application/account/ -count=1 -run 'OperationLog|RefreshToken|BatchRefresh|RefreshQuota' -v
GOCACHE=/root/Grok/grok2api/.gocache go test ./internal/transport/http/account/ -count=1 -run 'Operation' -v
# 若本机无 go：
docker run --rm -v /root/Grok/grok2api/backend:/src -w /src golang:1.26-alpine \
  go test ./internal/infra/persistence/relational/ -count=1 -run 'OperationLog' -v
```

**Frontend check:**
```bash
cd /root/Grok/grok2api/frontend && pnpm exec tsc --noEmit
```

---

## Phase 0 — 分支

### Task 0: 开 feature 分支

```bash
cd /root/Grok/grok2api
git checkout develop/20260802
git pull origin develop/20260802   # 若网络可用
git checkout -b feat/account-refresh-records
```

---

## Phase 1 — Domain + Schema + Repo（存储 seam）

### Task 1: Domain 类型与常量

**Files:**
- Create: `backend/internal/domain/account/operation_log.go`
- Test: `backend/internal/domain/account/operation_log_test.go`（可选纯常量校验；或直接 repo 测）

**类型（最小）：**
```go
package account

import "time"

type OperationType string
const (
	OperationQuotaSync          OperationType = "quota_sync"
	OperationCredentialRefresh  OperationType = "credential_refresh"
)

type OperationTrigger string
const (
	OperationTriggerManual    OperationTrigger = "manual"
	OperationTriggerBatch     OperationTrigger = "batch"
	OperationTriggerScheduler OperationTrigger = "scheduler"
)

const MaxOperationLogsPerType = 20
const MaxOperationRawResponseBytes = 4 << 10

type OperationLog struct {
	ID           uint64
	AccountID    uint64
	Provider     Provider
	OpType       OperationType
	Success      bool
	StatusCode   int
	ErrorCode    string
	Message      string
	RawResponse  string
	TriggeredBy  OperationTrigger
	StartedAt    time.Time
	FinishedAt   time.Time
	CreatedAt    time.Time
}

func TruncateOperationRawResponse(value string) string {
	if len(value) <= MaxOperationRawResponseBytes {
		return value
	}
	return value[:MaxOperationRawResponseBytes]
}
```

**Step:** 写 `TestTruncateOperationRawResponse` → 红 → 实现 → 绿 → commit  
`git commit -m "feat(account): 增加操作日志 domain 类型"`

---

### Task 2: Repository 接口

**Files:**
- Create 或 Modify: `backend/internal/repository/account_operation_log.go`（独立接口文件，避免撑爆 account.go）

```go
package repository

import (
	"context"
	"github.com/chenyme/grok2api/backend/internal/domain/account"
)

type AccountOperationLogRepository interface {
	Append(ctx context.Context, value account.OperationLog) (account.OperationLog, error)
	ListByAccount(ctx context.Context, accountID uint64, opType account.OperationType, limit int) ([]account.OperationLog, error)
	// LatestByAccountIDs 返回 map[accountID]latest；无记录的 id 不出现在 map 中。
	LatestByAccountIDs(ctx context.Context, accountIDs []uint64, opType account.OperationType) (map[uint64]account.OperationLog, error)
	// Trim 保证同 account+opType 最多保留 MaxOperationLogsPerType 条（按 finished_at desc, id desc）。
	Trim(ctx context.Context, accountID uint64, opType account.OperationType, keep int) error
}
```

**Commit:** `feat(account): 定义操作日志 repository 接口`

---

### Task 3: GORM model + schema 注册

**Files:**
- Modify: `backend/internal/infra/persistence/relational/models.go` — 增加 `accountOperationLogModel`
- Modify: `backend/internal/infra/persistence/relational/schema.go` — `schemaModels` 追加 model；`schemaIndexes` 追加：
  - `idx_account_operation_logs_account_op_finished ON account_operation_logs(account_id, op_type, finished_at DESC, id DESC)`

**Model 要点:**
- Table: `account_operation_logs`
- FK: `Account *accountModel` `OnDelete:CASCADE`
- checks: op_type IN (...), triggered_by IN (...), provider IN (...)
- raw_response text，message text

**Test:** 在 `repository_test.go` 风格的 helper 上 `openTestDatabase` 后确认表存在（可并入 Task 4）。

---

### Task 4: Repo 实现 + Trim 测试（核心 RED/GREEN）

**Files:**
- Create: `backend/internal/infra/persistence/relational/account_operation_log_repository.go`
- Create: `backend/internal/infra/persistence/relational/account_operation_log_repository_test.go`

**Test 清单（先写全再逐个绿也行，但推荐逐条 tracer）：**

1. `TestAppendAndListByAccount` — Append 2 条，List 按 finished_at desc  
2. `TestLatestByAccountIDs` — 多账号各取最新  
3. `TestTrimKeepsAtMost20` — 写入 21 条，Trim(20) 后 List 长度 20 且为最新  
4. `TestAppendCascadeDeleteWithAccount` — 删账号后日志清空（若 Delete 已 CASCADE）

**实现提示:**
- `Append` 内可自动 Truncate raw + 可选调用 Trim  
- 或 service 层 Append 后调 Trim；**推荐 repo.Append 成功后 best-effort Trim**，service 更简单  
- Latest: 可用子查询 / DISTINCT ON (postgres) / 分步查；SQLite 兼容优先「按 ids 查后内存取最新」若 ids 少（页大小），或窗口函数

**Run:**
```bash
go test ./internal/infra/persistence/relational/ -count=1 -run 'OperationLog' -v
```

**Commit:** `feat(account): 实现操作日志 repo 与 trim`

---

## Phase 2 — 写入钩子（credential + quota）

### Task 5: Service 注入 operationLogs + record helper

**Files:**
- Modify: `backend/internal/application/account/service.go`
  - `Service` 增加 `operationLogs repository.AccountOperationLogRepository`（可为 nil 兼容旧测）
  - `NewService` 或 `SetOperationLogRepository` setter（**推荐 setter**，少改构造签名与大量测试）
- Create: `backend/internal/application/account/operation_log.go`

```go
func (s *Service) recordOperation(ctx context.Context, log account.OperationLog) {
	if s == nil || s.operationLogs == nil {
		return
	}
	log.RawResponse = account.TruncateOperationRawResponse(log.RawResponse)
	if log.FinishedAt.IsZero() {
		log.FinishedAt = s.now().UTC()
	}
	if log.StartedAt.IsZero() {
		log.StartedAt = log.FinishedAt
	}
	if _, err := s.operationLogs.Append(ctx, log); err != nil {
		if s.logger != nil {
			s.logger.Error("append account operation log failed", "account_id", log.AccountID, "op_type", log.OpType, "err", err)
		}
	}
}
```

**Wire:** `backend/internal/app/application.go` — `NewAccountOperationLogRepository` + `SetOperationLogRepository`

**Commit:** `feat(account): 接入操作日志记录 helper`

---

### Task 6: 凭据刷新写 log（成功/失败/skipped）

**Files:**
- Modify: `service.go` 中 `RefreshToken`、失败路径、`BatchRefreshTokens` skipped 分支
- Modify: `credential_scheduler.go` 调度触发时 `TriggeredBy=scheduler`
- Test: `credential_refresh_test.go` 或新 `operation_log_credential_test.go`

**行为:**
| 路径 | success | error_code | triggered_by |
|---|---|---|---|
| 单条成功 | true | "" | manual（若从 HTTP）/ 由调用方传入 |
| 单条失败 | false | 现有 code | 同上 |
| batch skipped | false | `skipped` | batch |
| scheduler | * | * | scheduler |

**Seam 建议:** `refreshTokenWithTrigger(ctx, id, trigger)` 内部统一 record；对外 `RefreshToken` 默认 `manual`。

**Test 示例意图:**
```go
// 注入 fake operationLogs repo 或真实 sqlite
// RefreshToken 成功后 Latest 有 success=true, opType=credential_refresh
// 失败后 success=false, status/message 非空
// Batch 对无 refresh token 账号写 skipped
```

**Commit:** `feat(account): 凭据刷新写入操作日志`

---

### Task 7: 额度同步写 log

**Files:**
- Modify: `RefreshQuota` / `RefreshQuotaMode` 聚合成功失败、`BatchRefreshQuota`、Build billing 同步若算「额度同步」——**按设计：账号页「额度同步」按钮对应的路径都写 `quota_sync`**
  - Web/Console: `RefreshQuota` / batch refresh-quotas  
  - Build: `BatchRefreshBilling` / refresh billing（与 UI「额度同步」一致）
- 后台 web stale quota sync（`startup.go` / service 内）→ `scheduler`

**Test:** `operation_log_quota_test.go`
- Web RefreshQuota 成功 → quota_sync success  
- 失败 → success false + message  
- Batch 部分失败各有一条  

**Commit:** `feat(account): 额度同步写入操作日志`

---

## Phase 3 — 读 API

### Task 8: Service 列表与历史查询

**Files:**
- Modify: `service.go` 增加：
  - `ListOperationRecords(ctx, query) (items, total, error)`  
    - 基于 `accounts.List` + `operationLogs.LatestByAccountIDs`  
    - `result` 过滤：若在内存过滤会破坏分页 → **优先 SQL/二次查询**：  
      - never: 账号 id NOT IN (有 log 的)  
      - success/failed: join latest  
    - MVP 可接受：先 List 账号再 filter 再补页（文档注明局限）或一次实现正确分页  
  - `ListOperationLogs(ctx, accountID, opType) ([]OperationLog, error)` limit 20

**Query 结构:**
```go
type OperationRecordQuery struct {
	Provider account.Provider
	OpType   account.OperationType
	Result   string // "", success, failed, never
	// 复用 AccountListQuery 的 Search/Status/Page/PageSize/Sort...
}
```

**非法:** `OpType==credential_refresh && Provider!=build` → 返回 domain/service 错误，handler 映射 400

**Test:** service 或 handler 级

**Commit:** `feat(account): 操作记录列表与历史查询`

---

### Task 9: HTTP handlers

**Files:**
- Modify: `backend/internal/transport/http/account/handler.go`
  - **路由顺序注意：** `GET /accounts/operation-records` 必须在 `GET /accounts/:id` **之前**注册
  - `GET /accounts/:id/operation-logs`
- Test: `handler_test.go` 风格

**DTO:**
```go
type operationLogDTO struct {
	ID          string `json:"id"`
	OpType      string `json:"opType"`
	Success     bool   `json:"success"`
	StatusCode  int    `json:"statusCode"`
	ErrorCode   string `json:"errorCode,omitempty"`
	Message     string `json:"message,omitempty"`
	RawResponse string `json:"rawResponse,omitempty"`
	TriggeredBy string `json:"triggeredBy"`
	StartedAt   string `json:"startedAt"`
	FinishedAt  string `json:"finishedAt"`
}
```

**Commit:** `feat(account): 暴露 operation-records 与 operation-logs API`

---

## Phase 4 — 前端

### Task 10: API client + types + decoder

**Files:**
- Create: `frontend/src/features/account-refresh-records/account-refresh-records-api.ts`

复用 `@/shared/api/client` + decoder 模式（抄 `accounts-api.ts` 精简版）。

函数：
- `listOperationRecords(params)`
- `listOperationLogs(accountId, opType)`

**Commit:** `feat(frontend): 账号刷新记录 API client`

---

### Task 11: 路由 + 导航 + i18n 骨架

**Files:**
- Modify: `frontend/src/app/router.tsx`
- Modify: `frontend/src/app/deferred-pages.tsx` — `DeferredAccountRefreshRecordsPage`
- Modify: `frontend/src/app/app-shell.tsx` — navigation 项（插在 accounts 后），图标 `History` 或 `RotateCcw`
- Modify: `frontend/src/shared/i18n/index.ts` — `nav.accountRefreshRecords` + `accountRefreshRecords.*` 中英键

**最小页:** tabs 顶层 + 二级 + EmptyState「加载中/暂无」

**Commit:** `feat(frontend): 账号刷新记录路由与菜单`

---

### Task 12: 主表 + 结果筛选 + 历史弹层

**Files:**
- Create: `frontend/src/features/account-refresh-records/account-refresh-records-page.tsx`
- Create: `frontend/src/features/account-refresh-records/operation-history-dialog.tsx`（或同文件）

**行为:**
- 顶层 tab 切 opType；额度同步下切 provider  
- 凭据刷新强制 provider=build  
- 列：名称、状态、结果徽章、statusCode、message、triggeredBy、finishedAt、操作  
- 历史：Sheet，默认选最新  

复用：`DataTableShell`、`DataTableFilters`、`Pagination`、`AccountNameCell`（若合适）

**Commit:** `feat(frontend): 账号刷新记录列表与历史弹层`

---

### Task 13: 工具栏精简操作

**Files:** 同 page

复用 `accounts-api` 已有：
- `refreshAccountsQuota` / `refreshAccountsTokens` / cleanup / enable / delete  
按当前 tab 只暴露对应主按钮；进度 toast 对齐 accounts-page 精简拷贝（或抽 shared hook——**YAGNI：先拷必要片段，忌大重构**）

**Commit:** `feat(frontend): 账号刷新记录页批量运维按钮`

---

### Task 14: tsc + 手测清单

```bash
cd frontend && pnpm exec tsc --noEmit
```

手测见 design §10。Docker 重建可选：
```bash
cd /root/Grok/grok2api && docker compose build && docker compose up -d
```

---

## 垂直切片 ↔ Task 映射（供 to-issues）

| Issue 切片 | 覆盖 Tasks | 可独立验收 |
|---|---|---|
| **#A 存储基础** | 1–4 | repo 单测：Append/Latest/Trim20/级联删 |
| **#B 凭据刷新落日志** | 5–6 | RefreshToken/Batch/Scheduler 写 credential_refresh；含 skipped |
| **#C 额度同步落日志** | 7 | 三 provider 同步路径写 quota_sync |
| **#D 读 API** | 8–9 | operation-records + operation-logs + result 筛选 + 400 |
| **#E 前端页 E2E 可视** | 10–13 | 菜单进入、双层 Tab、列表、历史、工具栏 |
| **#F 联调验收** | 14 | 手测清单全过 |

依赖：A → B、C（可并行）→ D → E → F

---

## 实现原则

1. **日志失败不阻断主业务**  
2. **不改 lastRefresh* 语义**，只追加 log  
3. **路由注册顺序** 防 `:id` 吞掉 `operation-records`  
4. **最小 diff**；前端忌复制整个 2000 行 accounts-page  
5. 中文 commit message  

---

## 修订履历

| 日期 | 说明 |
|---|---|
| 2026-08-05 | 初版：对齐冻结设计；TDD 分 Phase；垂直切片映射 |
