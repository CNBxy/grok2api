import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { History, MoreHorizontal, RefreshCw, RotateCcw, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  cleanupAccounts,
  deleteAccounts,
  refreshAccountQuota,
  refreshAccountToken,
  refreshAccountsQuota,
  refreshAccountsTokens,
  updateAccountsEnabled,
  type AccountCleanupStatus,
  type AccountProvider,
} from "@/features/accounts/accounts-api";
import {
  listOperationLogs,
  listOperationRecords,
  operationResultLabel,
  type OperationLogDTO,
  type OperationRecordDTO,
  type OperationResultFilter,
  type OperationType,
} from "@/features/account-refresh-records/account-refresh-records-api";
import { EmptyState, ErrorState, LoadingState, TableLoadingRow } from "@/shared/components/data-state";
import { DataTableFilters } from "@/shared/components/data-table-filters";
import { DataTableShell } from "@/shared/components/data-table-shell";
import { PageHeader } from "@/shared/components/page-header";
import { Pagination } from "@/shared/components/pagination";
import { useDebouncedValue } from "@/shared/hooks/use-debounced-value";
import { cn } from "@/shared/lib/cn";
import { formatDateTime } from "@/shared/lib/format";

type CategoryTab = "quota_sync" | "credential_refresh";

const providers: AccountProvider[] = ["grok_build", "grok_web", "grok_console"];

export function AccountRefreshRecordsPage() {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const [category, setCategory] = useState<CategoryTab>("quota_sync");
  const [provider, setProvider] = useState<AccountProvider>("grok_build");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [resultFilter, setResultFilter] = useState<OperationResultFilter>("");
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const [historyTarget, setHistoryTarget] = useState<OperationRecordDTO | null>(null);
  const [selectedLogId, setSelectedLogId] = useState<string | null>(null);
  const [cleanupOpen, setCleanupOpen] = useState(false);
  const [batchDeleteOpen, setBatchDeleteOpen] = useState(false);
  const debouncedSearch = useDebouncedValue(search);

  const opType: OperationType = category === "credential_refresh" ? "credential_refresh" : "quota_sync";
  const activeProvider: AccountProvider = category === "credential_refresh" ? "grok_build" : provider;

  const recordsQuery = useQuery({
    queryKey: ["operation-records", opType, activeProvider, page, pageSize, debouncedSearch, statusFilter, resultFilter],
    queryFn: () => listOperationRecords({
      provider: activeProvider,
      opType,
      page,
      pageSize,
      search: debouncedSearch || undefined,
      status: statusFilter || undefined,
      result: resultFilter || undefined,
    }),
  });

  const historyQuery = useQuery({
    queryKey: ["operation-logs", historyTarget?.id, opType],
    queryFn: () => listOperationLogs(historyTarget!.id, opType),
    enabled: Boolean(historyTarget),
  });

  const items = recordsQuery.data?.items ?? [];
  const total = recordsQuery.data?.total ?? 0;
  const selectedIds = useMemo(() => Array.from(selected), [selected]);

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["operation-records"] });
    void queryClient.invalidateQueries({ queryKey: ["operation-logs"] });
    void queryClient.invalidateQueries({ queryKey: ["accounts"] });
  };

  const syncMutation = useMutation({
    mutationFn: async (ids: string[]) => {
      if (opType === "credential_refresh") return refreshAccountsTokens(ids, "grok_build");
      return refreshAccountsQuota(ids, activeProvider);
    },
    onSuccess: (result) => {
      invalidate();
      if ("skipped" in result) {
        toast.success(t("accounts.allTokensRefreshed", { succeeded: result.succeeded, failed: result.failed, skipped: result.skipped }));
      } else {
        toast.success(t("accounts.batchBillingRefreshed", { succeeded: result.succeeded, failed: result.failed }));
      }
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : t("errors.generic")),
  });

  const singleSyncMutation = useMutation({
    mutationFn: async (record: OperationRecordDTO) => {
      if (opType === "credential_refresh") return refreshAccountToken(record.id);
      return refreshAccountQuota(record.id);
    },
    onSuccess: () => {
      invalidate();
      toast.success(opType === "credential_refresh" ? t("accounts.authRefreshed") : t("accounts.billingRefreshed"));
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : t("errors.generic")),
  });

  const enableMutation = useMutation({
    mutationFn: ({ ids, enabled }: { ids: string[]; enabled: boolean }) => updateAccountsEnabled(ids, enabled, activeProvider),
    onSuccess: () => {
      setSelected(new Set());
      invalidate();
      toast.success(t("accounts.batchUpdated"));
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : t("errors.generic")),
  });

  const deleteMutation = useMutation({
    mutationFn: (ids: string[]) => deleteAccounts(ids, activeProvider),
    onSuccess: (result) => {
      setBatchDeleteOpen(false);
      setSelected(new Set());
      invalidate();
      toast.success(t("accounts.cleanupCompleted", { deleted: result.deleted }));
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : t("errors.generic")),
  });

  const cleanupMutation = useMutation({
    mutationFn: () => cleanupAccounts(activeProvider, ["disabled", "reauthRequired", "cooldown"] as AccountCleanupStatus[]),
    onSuccess: (result) => {
      setCleanupOpen(false);
      invalidate();
      toast.success(t("accounts.cleanupCompleted", { deleted: result.deleted }));
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : t("errors.generic")),
  });

  function switchCategory(next: CategoryTab) {
    setCategory(next);
    setPage(1);
    setSelected(new Set());
    if (next === "credential_refresh") setProvider("grok_build");
  }

  function switchProvider(next: AccountProvider) {
    setProvider(next);
    setPage(1);
    setSelected(new Set());
  }

  function toggleAll(checked: boolean) {
    setSelected(checked ? new Set(items.map((item) => item.id)) : new Set());
  }

  function toggleOne(id: string, checked: boolean) {
    setSelected((current) => {
      const next = new Set(current);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });
  }

  const selectedHistory = useMemo(() => {
    const logs = historyQuery.data ?? [];
    if (!logs.length) return null;
    return logs.find((item) => item.id === selectedLogId) ?? logs[0];
  }, [historyQuery.data, selectedLogId]);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-5">
      <PageHeader
        title={t("accountRefreshRecords.title")}
        description={t("accountRefreshRecords.description")}
        actions={(
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="outline" size="sm" disabled={!selectedIds.length || enableMutation.isPending} onClick={() => enableMutation.mutate({ ids: selectedIds, enabled: true })}>
              {t("common.enable")}
            </Button>
            <Button variant="outline" size="sm" disabled={!selectedIds.length || enableMutation.isPending} onClick={() => enableMutation.mutate({ ids: selectedIds, enabled: false })}>
              {t("common.disable")}
            </Button>
            <Button size="sm" disabled={!selectedIds.length || syncMutation.isPending} onClick={() => syncMutation.mutate(selectedIds)}>
              {syncMutation.isPending ? <Spinner className="size-3.5" /> : <RefreshCw className="size-3.5" />}
              {opType === "credential_refresh" ? t("accountRefreshRecords.refreshCredentials") : t("accountRefreshRecords.syncQuota")}
            </Button>
            <Button variant="outline" size="sm" onClick={() => setCleanupOpen(true)}>
              <RotateCcw className="size-3.5" />
              {t("accounts.cleanupAction")}
            </Button>
            <Button variant="destructive" size="sm" disabled={!selectedIds.length} onClick={() => setBatchDeleteOpen(true)}>
              <Trash2 className="size-3.5" />
              {t("common.delete")}
            </Button>
          </div>
        )}
      />

      <Tabs value={category} onValueChange={(value) => switchCategory(value as CategoryTab)}>
        <TabsList>
          <TabsTrigger value="quota_sync">{t("accountRefreshRecords.quotaSync")}</TabsTrigger>
          <TabsTrigger value="credential_refresh">{t("accountRefreshRecords.credentialRefresh")}</TabsTrigger>
        </TabsList>
      </Tabs>

      {category === "quota_sync" ? (
        <Tabs value={provider} onValueChange={(value) => switchProvider(value as AccountProvider)}>
          <TabsList>
            {providers.map((item) => (
              <TabsTrigger key={item} value={item}>{t(`accountRefreshRecords.providers.${item}`)}</TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      ) : (
        <p className="text-xs text-muted-foreground">{t("accountRefreshRecords.credentialRefreshHint")}</p>
      )}

      {selectedIds.length > 0 ? <p className="text-xs text-muted-foreground">{t("common.selectedCount", { count: selectedIds.length })}</p> : null}

      <DataTableShell
        toolbar={(
          <div className="flex w-full flex-wrap items-center gap-2">
            <Input
              value={search}
              onChange={(event) => { setSearch(event.target.value); setPage(1); }}
              placeholder={t("accounts.search")}
              className="h-8 w-full max-w-xs"
              aria-label={t("accounts.search")}
            />
            <DataTableFilters filters={[
              {
                id: "status",
                label: t("accounts.status"),
                value: statusFilter,
                onChange: (value) => { setStatusFilter(value); setPage(1); },
                options: [
                  { value: "active", label: t("accounts.statusActive") },
                  { value: "disabled", label: t("accounts.statusDisabled") },
                  { value: "reauthRequired", label: t("accounts.statusReauthRequired") },
                ],
              },
              {
                id: "result",
                label: t("accountRefreshRecords.result"),
                value: resultFilter,
                onChange: (value) => { setResultFilter(value as OperationResultFilter); setPage(1); },
                options: [
                  { value: "success", label: t("accountRefreshRecords.results.success") },
                  { value: "failed", label: t("accountRefreshRecords.results.failed") },
                  { value: "never", label: t("accountRefreshRecords.results.never") },
                ],
              },
            ]}
            />
          </div>
        )}
        footer={total > 0 ? (
          <Pagination page={page} pageSize={pageSize} total={total} onPageChange={setPage} onPageSizeChange={(value) => { setPageSize(value); setPage(1); }} />
        ) : null}
      >
        {recordsQuery.isLoading ? <LoadingState /> : null}
        {recordsQuery.isError ? <ErrorState message={recordsQuery.error instanceof Error ? recordsQuery.error.message : t("errors.generic")} onRetry={() => void recordsQuery.refetch()} /> : null}
        {!recordsQuery.isLoading && !recordsQuery.isError && items.length === 0 ? <EmptyState message={t("common.noData")} /> : null}
        {!recordsQuery.isLoading && !recordsQuery.isError && items.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10">
                  <Checkbox checked={items.length > 0 && items.every((item) => selected.has(item.id))} onCheckedChange={(value) => toggleAll(Boolean(value))} aria-label={t("common.selectPage")} />
                </TableHead>
                <TableHead>{t("accounts.account")}</TableHead>
                <TableHead>{t("accounts.status")}</TableHead>
                <TableHead>{t("accountRefreshRecords.result")}</TableHead>
                <TableHead>{t("accountRefreshRecords.statusCode")}</TableHead>
                <TableHead>{t("accountRefreshRecords.message")}</TableHead>
                <TableHead>{t("accountRefreshRecords.triggeredBy")}</TableHead>
                <TableHead>{t("accountRefreshRecords.finishedAt")}</TableHead>
                <TableHead className="w-28">{t("common.actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {recordsQuery.isFetching && !recordsQuery.isLoading ? <TableLoadingRow colSpan={9} /> : null}
              {items.map((item) => {
                const result = operationResultLabel(item);
                return (
                  <TableRow key={item.id}>
                    <TableCell>
                      <Checkbox checked={selected.has(item.id)} onCheckedChange={(value) => toggleOne(item.id, Boolean(value))} aria-label={t("common.selectItem", { name: item.name })} />
                    </TableCell>
                    <TableCell>
                      <div className="min-w-0">
                        <div className="truncate font-medium">{item.name}</div>
                        {item.email ? <div className="truncate text-xs text-muted-foreground">{item.email}</div> : null}
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline">{item.enabled ? (item.authStatus === "reauthRequired" ? t("accounts.statusReauthRequired") : t("accounts.statusActive")) : t("accounts.statusDisabled")}</Badge>
                    </TableCell>
                    <TableCell><ResultBadge result={result} /></TableCell>
                    <TableCell className="tabular-nums text-muted-foreground">{item.latestOperation?.statusCode || "—"}</TableCell>
                    <TableCell className="max-w-[220px] truncate text-muted-foreground" title={item.latestOperation?.message}>{item.latestOperation?.message || "—"}</TableCell>
                    <TableCell className="text-muted-foreground">{item.latestOperation ? t(`accountRefreshRecords.triggers.${item.latestOperation.triggeredBy}`) : "—"}</TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">{item.latestOperation ? formatDateTime(item.latestOperation.finishedAt, i18n.language) : "—"}</TableCell>
                    <TableCell>
                      <div className="flex items-center gap-1">
                        <Button variant="ghost" size="icon" className="size-7" disabled={singleSyncMutation.isPending} onClick={() => singleSyncMutation.mutate(item)} aria-label={t("accountRefreshRecords.retry")}>
                          <RefreshCw className="size-3.5" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7"
                          onClick={() => {
                            setHistoryTarget(item);
                            setSelectedLogId(item.latestOperation?.id ?? null);
                          }}
                          aria-label={t("accountRefreshRecords.history")}
                        >
                          <History className="size-3.5" />
                        </Button>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button variant="ghost" size="icon" className="size-7" aria-label={t("common.actions")}><MoreHorizontal className="size-3.5" /></Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem onClick={() => enableMutation.mutate({ ids: [item.id], enabled: !item.enabled })}>
                              {item.enabled ? t("common.disable") : t("common.enable")}
                            </DropdownMenuItem>
                            <DropdownMenuSeparator />
                            <DropdownMenuItem className="text-destructive" onClick={() => { setSelected(new Set([item.id])); setBatchDeleteOpen(true); }}>
                              {t("common.delete")}
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        ) : null}
      </DataTableShell>

      <Sheet open={Boolean(historyTarget)} onOpenChange={(open) => { if (!open) setHistoryTarget(null); }}>
        <SheetContent className="flex w-full flex-col gap-4 sm:max-w-xl">
          <SheetHeader>
            <SheetTitle>{t("accountRefreshRecords.historyTitle", { name: historyTarget?.name ?? "" })}</SheetTitle>
            <SheetDescription>{t("accountRefreshRecords.historyDescription")}</SheetDescription>
          </SheetHeader>
          {historyQuery.isLoading ? <LoadingState /> : null}
          {historyQuery.isError ? <ErrorState message={historyQuery.error instanceof Error ? historyQuery.error.message : t("errors.generic")} onRetry={() => void historyQuery.refetch()} /> : null}
          {!historyQuery.isLoading && !historyQuery.isError ? (
            <div className="grid min-h-0 flex-1 gap-4 overflow-hidden md:grid-cols-[180px_1fr]">
              <div className="min-h-0 space-y-1 overflow-y-auto rounded-md border p-2">
                {(historyQuery.data ?? []).length === 0 ? <p className="p-2 text-xs text-muted-foreground">{t("accountRefreshRecords.noHistory")}</p> : null}
                {(historyQuery.data ?? []).map((log) => (
                  <button
                    key={log.id}
                    type="button"
                    className={cn(
                      "flex w-full flex-col rounded-md px-2 py-1.5 text-left text-xs transition-colors hover:bg-secondary/60",
                      (selectedHistory?.id ?? "") === log.id && "bg-secondary/70",
                    )}
                    onClick={() => setSelectedLogId(log.id)}
                  >
                    <span className="font-medium">{formatDateTime(log.finishedAt, i18n.language)}</span>
                    <span className="text-muted-foreground">{log.success ? t("accountRefreshRecords.results.success") : (log.errorCode === "skipped" ? t("accountRefreshRecords.results.skipped") : t("accountRefreshRecords.results.failed"))}</span>
                  </button>
                ))}
              </div>
              <div className="min-h-0 overflow-y-auto rounded-md border p-3 text-sm">
                {selectedHistory ? <HistoryDetail log={selectedHistory} locale={i18n.language} /> : <p className="text-muted-foreground">{t("accountRefreshRecords.noHistory")}</p>}
              </div>
            </div>
          ) : null}
        </SheetContent>
      </Sheet>

      <AlertDialog open={batchDeleteOpen} onOpenChange={setBatchDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("accounts.batchDeleteTitle", { count: selectedIds.length })}</AlertDialogTitle>
            <AlertDialogDescription>{t("accounts.deleteDescription")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction disabled={deleteMutation.isPending} onClick={() => deleteMutation.mutate(selectedIds)}>{t("common.delete")}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={cleanupOpen} onOpenChange={setCleanupOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("accounts.cleanupTitle", { provider: t(`accountRefreshRecords.providers.${activeProvider}`) })}</AlertDialogTitle>
            <AlertDialogDescription>{t("accounts.cleanupDescription")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction disabled={cleanupMutation.isPending} onClick={() => cleanupMutation.mutate()}>{t("accounts.cleanupStart")}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function ResultBadge({ result }: { result: "success" | "failed" | "skipped" | "never" }) {
  const { t } = useTranslation();
  const className = {
    success: "border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
    failed: "border-red-500/40 bg-red-500/10 text-red-700 dark:text-red-300",
    skipped: "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300",
    never: "text-muted-foreground",
  }[result];
  return <Badge variant="outline" className={className}>{t(`accountRefreshRecords.results.${result}`)}</Badge>;
}

function HistoryDetail({ log, locale }: { log: OperationLogDTO; locale: string }) {
  const { t } = useTranslation();
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 gap-2 text-xs">
        <Detail label={t("accountRefreshRecords.result")} value={log.success ? t("accountRefreshRecords.results.success") : (log.errorCode === "skipped" ? t("accountRefreshRecords.results.skipped") : t("accountRefreshRecords.results.failed"))} />
        <Detail label={t("accountRefreshRecords.statusCode")} value={String(log.statusCode || "—")} />
        <Detail label={t("accountRefreshRecords.errorCode")} value={log.errorCode || "—"} />
        <Detail label={t("accountRefreshRecords.triggeredBy")} value={t(`accountRefreshRecords.triggers.${log.triggeredBy}`)} />
        <Detail label={t("accountRefreshRecords.startedAt")} value={formatDateTime(log.startedAt, locale)} />
        <Detail label={t("accountRefreshRecords.finishedAt")} value={formatDateTime(log.finishedAt, locale)} />
      </div>
      <div>
        <div className="mb-1 text-xs text-muted-foreground">{t("accountRefreshRecords.message")}</div>
        <pre className="whitespace-pre-wrap break-words rounded-md bg-muted/50 p-2 text-xs">{log.message || "—"}</pre>
      </div>
      {log.rawResponse ? (
        <div>
          <div className="mb-1 text-xs text-muted-foreground">{t("accountRefreshRecords.rawResponse")}</div>
          <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted/50 p-2 text-xs">{log.rawResponse}</pre>
        </div>
      ) : null}
    </div>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-muted-foreground">{label}</div>
      <div className="font-medium text-foreground">{value}</div>
    </div>
  );
}
