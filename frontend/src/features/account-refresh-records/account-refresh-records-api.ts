import { apiRequest, type PaginatedDTO } from "@/shared/api/client";
import { createObjectDecoder, createPaginatedDecoder, createValidatedDecoder, hasShape, isArrayOf, isBoolean, isNumber, isOneOf, isOptional, isString } from "@/shared/api/decoder";
import type { AccountProvider } from "@/features/accounts/accounts-api";
import type { SortOrder } from "@/shared/lib/table-sort";

export type OperationType = "quota_sync" | "credential_refresh";
export type OperationResultFilter = "" | "success" | "failed" | "never";
export type OperationTrigger = "manual" | "batch" | "scheduler";

export type OperationLogDTO = {
  id: string;
  opType: OperationType;
  success: boolean;
  statusCode: number;
  errorCode?: string;
  message?: string;
  rawResponse?: string;
  triggeredBy: OperationTrigger;
  startedAt: string;
  finishedAt: string;
};

export type OperationRecordDTO = {
  id: string;
  provider: AccountProvider;
  name: string;
  email?: string;
  enabled: boolean;
  authStatus: "active" | "reauthRequired";
  refreshable: boolean;
  createdAt: string;
  latestOperation: OperationLogDTO | null;
};

const operationLogValidator = hasShape({
  id: isString,
  opType: isOneOf("quota_sync", "credential_refresh"),
  success: isBoolean,
  statusCode: isNumber,
  errorCode: isOptional(isString),
  message: isOptional(isString),
  rawResponse: isOptional(isString),
  triggeredBy: isOneOf("manual", "batch", "scheduler"),
  startedAt: isString,
  finishedAt: isString,
});

const operationRecordValidator = hasShape({
  id: isString,
  provider: isOneOf("grok_build", "grok_web", "grok_console"),
  name: isString,
  email: isOptional(isString),
  enabled: isBoolean,
  authStatus: isOneOf("active", "reauthRequired"),
  refreshable: isBoolean,
  createdAt: isString,
  latestOperation: (value) => value === null || operationLogValidator(value),
});

const decodeOperationRecordPage = createPaginatedDecoder<OperationRecordDTO>(operationRecordValidator);
const decodeOperationLogList = createObjectDecoder<{ items: OperationLogDTO[] }>("operation logs", {
  items: isArrayOf(operationLogValidator),
});

export type ListOperationRecordsInput = {
  provider: AccountProvider;
  opType: OperationType;
  page: number;
  pageSize: number;
  search?: string;
  status?: string;
  result?: OperationResultFilter;
  sortBy?: string;
  sortOrder?: SortOrder;
};

export function listOperationRecords(input: ListOperationRecordsInput): Promise<PaginatedDTO<OperationRecordDTO>> {
  const query = new URLSearchParams({
    page: String(input.page),
    pageSize: String(input.pageSize),
    provider: input.provider,
    opType: input.opType,
  });
  if (input.search) query.set("search", input.search);
  if (input.status) query.set("status", input.status);
  if (input.result) query.set("result", input.result);
  if (input.sortBy && input.sortOrder) {
    query.set("sortBy", input.sortBy);
    query.set("sortOrder", input.sortOrder);
  }
  return apiRequest(`/api/admin/v1/accounts/operation-records?${query}`, {}, decodeOperationRecordPage);
}

export function listOperationLogs(accountId: string, opType: OperationType): Promise<OperationLogDTO[]> {
  const query = new URLSearchParams({ opType });
  return apiRequest(`/api/admin/v1/accounts/${accountId}/operation-logs?${query}`, {}, decodeOperationLogList).then((value) => value.items);
}

export function operationResultLabel(record: OperationRecordDTO): "success" | "failed" | "skipped" | "never" {
  const latest = record.latestOperation;
  if (!latest) return "never";
  if (latest.success) return "success";
  if (latest.errorCode === "skipped") return "skipped";
  return "failed";
}

void createValidatedDecoder;
void createObjectDecoder;
