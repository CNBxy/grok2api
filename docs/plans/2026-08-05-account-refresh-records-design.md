# 账号刷新记录（Account Refresh Records）设计

**日期**: 2026-08-05  
**状态**: 已冻结（grill + superpowers brainstorm 全节审批通过）  
**项目**: grok2api（`/root/Grok/grok2api`）  
**分支建议**: 自 `develop/20260802` 开 feature 分支实现  

## 1. 背景与目标

管理端已有：

- Grok Build / Web / Console **额度同步**
- Grok Build **凭据刷新**（含后台调度）
- 账号页 `/accounts` 上的批量操作与进度

痛点：额度同步与凭据刷新的**结果分散**，失败时状态码 / 上游 message / 时间不易按分类排查；缺少「按操作类型」的运维结果视图与历史。

**目标**：左侧抽屉新增独立页面 **账号刷新记录**，按「额度同步 / 凭据刷新」分类展示每个账号**最近一次**结果，支持成功/失败筛选、批量再操作，并以完整历史日志（默认最新 + 弹层时间线）支持追溯。

## 2. 非目标（YAGNI）

- 不改造现有 `/accounts` 为结果中心；账号导入 / 设备授权 / 导出 / 转换 / Web 脚本 / 出口绑定仍只在账号页
- 不做跨 opType 混合列表
- 不改网关路由选号逻辑
- 不强制为额度同步新增账号表 `lastQuota*` 列（latest 以操作日志为准）
- 本期不做无限历史、跨实例审计导出

## 3. 已冻结需求共识

| 项 | 结论 |
|---|---|
| 页面 | 独立路由 `/account-refresh-records` |
| 分类 UI | 顶层 Tab：额度同步 \| 凭据刷新；额度同步下二级：build / web / console；凭据刷新仅 build |
| 主表行实体 | 当前小分类下**全部账号** + 该 opType **最新一次**结果；无记录 = 未操作 |
| 历史 | 完整日志；默认展示最新；行内「历史」弹层时间线点选 |
| 写入范围 | 手动 / 批量 / 后台调度 **全写** |
| skipped | **也写日志**：`success=false`，`error_code=skipped` |
| 保留 | 每账号 × 操作类型最多 **20** 条，超限删最旧 |
| 工具栏 | 精简：筛选 + 额度同步或凭据刷新 + 清理 + 启停 + 删除 |
| 批量对象 | 账号；操作仅针对**当前小分类** provider 下的对象 |

## 4. 方案选择

**采用方案 A：独立操作日志表 + 账号列表联查。**

| 方案 | 结论 |
|---|---|
| A 独立 `account_operation_logs` | **选用** |
| B 仅扩展 last* 无历史 | 否决（不满足完整历史） |
| C 复用 request audits | 否决（语义为网关请求审计，非管理端运维） |

## 5. 架构

```
[AppShell 导航]
  + 账号刷新记录  →  /account-refresh-records

[Frontend]
  features/account-refresh-records/
    page: 顶层 Tab(额度同步|凭据刷新)
          └ 额度同步 → 二级 Tab(build|web|console)
    主表: 账号行 + 最新操作结果 + 筛选/多选
    弹层: 该账号×opType 历史时间线（≤20）
    写操作: 复用现有 account batch APIs

[Backend]
  domain: AccountOperationLog
  repo: Append + ListByAccount + LatestByAccountIDs + TrimToLimit(20)
  service 钩子:
    RefreshToken / BatchRefreshTokens / credential scheduler
    RefreshQuota / BatchRefreshQuota / web 后台额度同步 等
    → 成功/失败/skipped 均 Append
  新读 API:
    GET /api/admin/v1/accounts/operation-records
    GET /api/admin/v1/accounts/:id/operation-logs
  凭据 lastRefresh* 字段保留（账号页与调度逻辑不动）
```

### 数据流（一次额度同步）

1. UI 调用现有 `POST ...` 批量/单条额度同步接口  
2. service 对每个 id 执行 Sync，更新账号额度状态  
3. **紧随其后** Append `quota_sync` 日志（success/fail/skipped + code/message）  
4. Trim 该 `account_id × quota_sync` 超出 20 的旧行（best-effort）  
5. 本页 invalidate 后刷新 latest 视图  

凭据刷新同理，opType=`credential_refresh`，并继续维护 `Credential.LastRefresh*`。

## 6. 数据模型

### 表 `account_operation_logs`

| 列 | 类型 | 说明 |
|---|---|---|
| id | uint64 PK | |
| account_id | uint64 FK → accounts | 账号删除时 **级联删除** 日志 |
| provider | string | `grok_build` / `grok_web` / `grok_console` |
| op_type | string | `quota_sync` \| `credential_refresh` |
| success | bool | skipped 为 false |
| status_code | int | HTTP/上游码；无则 0 |
| error_code | string | 业务码；skipped 时为 `skipped` |
| message | string | 失败/跳过文案优先；成功可空或短摘要 |
| raw_response | text | 截断后原始响应，建议 ≤ **4KB**，可空 |
| triggered_by | string | `manual` \| `batch` \| `scheduler` |
| started_at | timestamptz | |
| finished_at | timestamptz | |
| created_at | timestamptz | 写入时间 |

### 索引

- `(account_id, op_type, finished_at DESC)` — 历史弹层 + 取 latest  
- 可选：`(provider, op_type, finished_at DESC)`  

### 列表实现建议

1. 按现有账号 list 条件（provider、搜索、启用状态等）分页查 accounts  
2. 对当页 ids **批量**查各 id 在当前 opType 下最新一条 log  
3. `result` 过滤：  
   - `success`：latest.success == true  
   - `failed`：latest 存在且 success == false（含 skipped）  
   - `never`：无 log  
   - 空：全部  

> 若 `result` 过滤导致「先分页再过滤」不准，应在 SQL/仓库层用 LEFT JOIN latest + WHERE 再分页（实现阶段按此优先）。

### 裁剪

- 常量：`MaxAccountOperationLogsPerType = 20`  
- Append 后删除同 `account_id + op_type` 中按 `finished_at DESC, id DESC` 排名 > 20 的行  

### 与现有字段

| 能力 | 策略 |
|---|---|
| 凭据刷新 lastRefresh* | **继续写**（调度/账号页依赖）+ 额外 Append log |
| 额度同步 lastQuota* | **不新增**账号列；UI latest 只读 log |
| 账号删除 | 日志级联删除 |

### 成功 / 失败 / skipped

- 业务成功 → `success=true`  
- 上游/业务错误 → `success=false`，尽量填 `status_code` / `error_code` / `message` / `raw_response`  
- batch **skipped**（如无 refresh token、provider 不支持）→ **写 log**：`success=false`，`error_code=skipped`，message 说明原因  

## 7. API

### 7.1 列表

`GET /api/admin/v1/accounts/operation-records`

**Query（核心）**

| 参数 | 说明 |
|---|---|
| opType | 必填：`quota_sync` \| `credential_refresh` |
| provider | 必填；`credential_refresh` 仅允许 `grok_build` |
| page / pageSize | 分页 |
| search | 名称/邮箱等，对齐账号页 |
| status 等 | 与账号页通用筛选对齐（实现时取精简子集） |
| result | `""` \| `success` \| `failed` \| `never` |
| sortBy / sortOrder | 默认建议按 latest `finishedAt` desc；never 沉底或按账号 `createdAt` |

**非法组合**（如 `credential_refresh` + `grok_web`）→ **400**。

**Response item**

精简账号字段 +：

```json
{
  "latestOperation": null,
  "latestOperation": {
    "id": "...",
    "opType": "quota_sync",
    "success": false,
    "statusCode": 401,
    "errorCode": "unauthorized",
    "message": "...",
    "rawResponse": "...",
    "triggeredBy": "batch",
    "startedAt": "...",
    "finishedAt": "..."
  }
}
```

### 7.2 历史

`GET /api/admin/v1/accounts/:id/operation-logs?opType=quota_sync`

- 最多 20 条，`finishedAt` desc  
- 账号不存在 → 404  

### 7.3 写路径（复用，不新造业务写接口）

| 本页按钮 | 现有能力 |
|---|---|
| 额度同步（选中/全部） | 现有 batch / all quota（及 build billing 若当前分类需要，与账号页同 provider 语义对齐） |
| 凭据刷新 | `batch/refresh-tokens`、单条 refresh 等 |
| 清理 | 现有 cleanup |
| 启停 / 删除 | 现有 batch enabled / delete |

Service 内在结果出口 Append log；`triggeredBy`：

- 单条 UI → `manual`  
- 批量 API → `batch`  
- 后台调度 → `scheduler`  

## 8. UI

### 导航

- `app-shell` 增加 `/account-refresh-records`  
- i18n：`nav.accountRefreshRecords`（中：账号刷新记录 / 英：Account Refresh Records）  
- 图标：`RotateCcw` 或 `History`  
- `router` + `deferred-pages` 懒加载  

### 页布局

```
PageHeader
Tabs 顶层: [额度同步] [凭据刷新]
  └ 额度同步: 二级 [Build] [Web] [Console]
  └ 凭据刷新: 无二级（provider=grok_build）

DataTableFilters: 搜索 | 启用状态 | 结果(全部/成功/失败/未操作) | …

工具栏:
  选中计数 | 启用 | 禁用 | 额度同步或凭据刷新 | 清理 | 删除
  （可选）同步/刷新全部 + 二次确认

Table:
  ☑ | 名称/邮箱 | Provider | 启用/认证
  | 结果徽章 | 状态码 | Message | 触发来源 | 完成时间
  | 单条同步或刷新 | 历史 | 更多(启停/删除)

历史 Sheet/Dialog:
  时间线 ≤20 → 点选详情（含 rawResponse）
  默认选中最新
```

### 视觉

- 成功：绿  
- 失败：红  
- skipped：琥珀（可归入失败筛选，徽章可单独文案）  
- 未操作：灰 muted  

### 交互

- 批量进度：对齐账号页 toast / 事件流进度  
- 完成后 invalidate operation-records  
- 历史加载失败：局部错误，不卸主表  

## 9. 错误处理

| 场景 | 策略 |
|---|---|
| 日志 Append 失败 | **不阻断**主业务；打 error 日志 |
| Trim 失败 | best-effort，不阻断 |
| raw_response 过大 | 截断 4KB |
| 非法 opType/provider | 400 |
| 并发多写 | 允许多条；Trim 保证 ≤20 |
| 批量 partial fail | 现有 succeeded/failed/skipped 汇总 toast |

## 10. 测试策略（TDD seams）

### 后端

1. Repo：Append、ListByAccount、LatestByIDs、Trim 边界（20/21）  
2. RefreshToken：成功 / 失败 / permanent 均写 log  
3. Batch 中 skipped → `error_code=skipped`  
4. RefreshQuota / BatchRefreshQuota → `quota_sync`  
5. Scheduler → `triggeredBy=scheduler`  
6. operation-records：`result=success|failed|never`  
7. 账号删除级联清日志  
8. Handler：非法组合 400  

### 前端

- DTO decoder/validator  
- 手测验收（见下）；有前端单测习惯时可补徽章映射  

### 手测验收清单

1. 菜单进入新页，双层 Tab 正确  
2. Build 额度同步一批 → 成功绿、失败红 + code/message  
3. 历史弹层默认最新，可点旧记录  
4. 同账号同 opType >20 次 → 仅留 20  
5. 凭据刷新仅 Build  
6. 后台调度后可见 `scheduler`  
7. 清理 / 启停 / 删除仅作用于当前小分类，行为与账号页一致  
8. skipped 账号出现在失败筛选，徽章可识别  

## 11. 实现切片顺序

1. domain + schema 迁移 + repo + trim 测试  
2. service 钩子写 log（quota + credential + scheduler + skipped）  
3. list / history HTTP API + 契约测试  
4. 前端页 + 导航 + i18n + 历史弹层  
5. 联调与手测验收  

## 12. 修订履历

| 日期 | 说明 |
|---|---|
| 2026-08-05 | 初版冻结：grill 共识 + 方案 A + §1–§5 全节审批通过；skipped 写失败日志；保留上限 20 |
