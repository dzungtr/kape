import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { LoaderFunctionArgs } from "react-router";
import {
  useLoaderData,
  useSearchParams,
  useNavigate,
  useRevalidator,
} from "react-router";
import { listTasks } from "~/lib/api.server";
import type { Task, TaskList } from "~/lib/api.server";
import { shortId, canTimeout, canRetry } from "~/lib/utils";
import { StatusBadge } from "~/components/StatusBadge";
import { ElapsedTimer } from "~/components/ElapsedTimer";
import { ConfirmModal } from "~/components/ConfirmModal";
import { FilterBar } from "~/components/FilterBar";
import { BulkActionBar } from "~/components/BulkActionBar";
import { LiveIndicator } from "~/components/LiveIndicator";

// ── SSE client import (may be typed as any) ──────────────────────────────
import { useEventSource } from "remix-utils/sse/react";

// ── Loader ────────────────────────────────────────────────────────────────

export async function loader({ request }: LoaderFunctionArgs) {
  const url = new URL(request.url);
  const handler = url.searchParams.get("handler") ?? undefined;
  const status = url.searchParams.get("status") ?? undefined;
  const since = url.searchParams.get("since") ?? undefined;
  const cursor = url.searchParams.get("cursor") ?? undefined;

  const data: TaskList = await listTasks({
    handler,
    status,
    since,
    sort: "received_at:desc",
    limit: 50,
    cursor: cursor ?? undefined,
  });

  return { data };
}

// ── SSE Event type ────────────────────────────────────────────────────────

interface SseEvent {
  type: "task.created" | "task.updated";
  task: Task;
}

// ── Component ─────────────────────────────────────────────────────────────

export default function TasksIndex() {
  const { data: initialData } = useLoaderData() as { data: TaskList };
  const revalidator = useRevalidator();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  // ── SSE connection ────────────────────────────────────────────────────
  const sseData = useEventSource("/sse/tasks", {
    event: "task.created",
  }) as string | null;
  const sseUpdatedData = useEventSource("/sse/tasks", {
    event: "task.updated",
  }) as string | null;

  // Combine both SSE streams into one processor
  const [isConnected, setIsConnected] = useState(true);
  const [isPaused, setIsPaused] = useState(false);
  const bufferedEvents = useRef<SseEvent[]>([]);

  // Track SSE connection state from useEventSource
  const lastCreatedEvent = sseData;
  const lastUpdatedEvent = sseUpdatedData;

  // ── Task feed state: Keyed by task ID for de-dup ──────────────────────
  const taskMapRef = useRef<Map<string, Task>>(new Map());

  // Initialize task map from SSR loader data
  useEffect(() => {
    const map = new Map<string, Task>();
    for (const task of initialData.tasks) {
      map.set(task.id, task);
    }
    taskMapRef.current = map;
  }, [initialData.tasks]);

  const [tasks, setTasks] = useState<Task[]>(initialData.tasks);
  const [nextCursor, setNextCursor] = useState<string | null | undefined>(
    initialData.next_cursor
  );
  const [loadingOlder, setLoadingOlder] = useState(false);

  // Sort task map values by received_at descending
  const sortTasksDesc = (map: Map<string, Task>): Task[] => {
    const arr = Array.from(map.values());
    arr.sort(
      (a, b) =>
        new Date(b.received_at).getTime() -
        new Date(a.received_at).getTime()
    );
    return arr;
  };

  // Process SSE events: upsert by ID
  const processSseEvent = useCallback((eventStr: string) => {
    try {
      const task = JSON.parse(eventStr) as Task;
      const map = taskMapRef.current;
      map.set(task.id, task);
      setTasks(sortTasksDesc(map));
    } catch {
      // Ignore parse errors from SSE
    }
  }, []);

  // Handle task.created events
  const prevCreatedRef = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (lastCreatedEvent != null && lastCreatedEvent !== prevCreatedRef.current) {
      prevCreatedRef.current = lastCreatedEvent;
      if (isPaused) {
        bufferedEvents.current.push({
          type: "task.created",
          task: JSON.parse(lastCreatedEvent) as Task,
        });
      } else {
        processSseEvent(lastCreatedEvent);
      }
    }
  }, [lastCreatedEvent, isPaused, processSseEvent]);

  // Handle task.updated events
  const prevUpdatedRef = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (lastUpdatedEvent != null && lastUpdatedEvent !== prevUpdatedRef.current) {
      prevUpdatedRef.current = lastUpdatedEvent;
      if (isPaused) {
        bufferedEvents.current.push({
          type: "task.updated",
          task: JSON.parse(lastUpdatedEvent) as Task,
        });
      } else {
        processSseEvent(lastUpdatedEvent);
      }
    }
  }, [lastUpdatedEvent, isPaused, processSseEvent]);

  // Monitor SSE connection (assume connected if data arrives)
  useEffect(() => {
    if (sseData != null || sseUpdatedData != null) {
      setIsConnected(true);
    }
  }, [sseData, sseUpdatedData]);

  // ── Pause / Resume ────────────────────────────────────────────────────
  const handleTogglePause = useCallback(() => {
    if (!isPaused) {
      setIsPaused(true);
    }
  }, [isPaused]);

  const handleResume = useCallback(() => {
    setIsPaused(false);
    const buffered = bufferedEvents.current;
    bufferedEvents.current = [];
    const map = taskMapRef.current;
    for (const event of buffered) {
      map.set(event.task.id, event.task);
    }
    setTasks(sortTasksDesc(map));
  }, []);

  const bufferedCount = bufferedEvents.current.length;

  // ── Infinite scroll: load older pages ─────────────────────────────────
  const loadMore = useCallback(async () => {
    if (!nextCursor || loadingOlder) return;
    setLoadingOlder(true);
    try {
      const params = new URLSearchParams(searchParams);
      const handler = params.get("handler") ?? undefined;
      const status = params.get("status") ?? undefined;
      const since = params.get("since") ?? undefined;

      const data = await listTasks({
        handler: handler ?? undefined,
        status: status ?? undefined,
        since: since ?? undefined,
        sort: "received_at:desc",
        limit: 50,
        cursor: nextCursor,
      });

      // Merge into map (de-dup by ID)
      const map = taskMapRef.current;
      for (const task of data.tasks) {
        if (!map.has(task.id)) {
          map.set(task.id, task);
        }
      }
      setTasks(sortTasksDesc(map));
      setNextCursor(data.next_cursor);
    } catch (err) {
      console.error("Failed to load older tasks:", err);
    } finally {
      setLoadingOlder(false);
    }
  }, [nextCursor, loadingOlder, searchParams]);

  // ── Selection (bulk timeout) ──────────────────────────────────────────
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());

  const toggleSelect = useCallback((taskId: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(taskId)) {
        next.delete(taskId);
      } else {
        next.add(taskId);
      }
      return next;
    });
  }, []);

  const clearSelection = useCallback(() => {
    setSelectedIds(new Set());
  }, []);

  // ── Modals ────────────────────────────────────────────────────────────
  const [bulkTimeoutOpen, setBulkTimeoutOpen] = useState(false);
  const [singleTimeoutOpen, setSingleTimeoutOpen] = useState(false);
  const [retryOpen, setRetryOpen] = useState(false);
  const [actionTask, setActionTask] = useState<Task | null>(null);

  const handleSingleTimeout = useCallback((task: Task) => {
    setActionTask(task);
    setSingleTimeoutOpen(true);
  }, []);

  const handleRetry = useCallback((task: Task) => {
    setActionTask(task);
    setRetryOpen(true);
  }, []);

  const executeTimeout = useCallback(
    async (taskId: string) => {
      const baseUrl = process.env.TASK_SERVICE_URL ?? "http://localhost:8080";
      await fetch(`${baseUrl}/tasks/${taskId}/status`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status: "Timeout" }),
      });
      revalidator.revalidate();
    },
    [revalidator]
  );

  const executeBulkTimeout = useCallback(async () => {
    const baseUrl = process.env.TASK_SERVICE_URL ?? "http://localhost:8080";
    await fetch(`${baseUrl}/tasks/bulk/status`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        ids: Array.from(selectedIds),
        status: "Timeout" as const,
      }),
    });
    clearSelection();
    revalidator.revalidate();
  }, [selectedIds, clearSelection, revalidator]);

  // ── Derived data ──────────────────────────────────────────────────────
  const handlerOptions = useMemo(() => {
    const handlers = new Set<string>();
    for (const t of tasks) {
      handlers.add(t.handler);
    }
    return Array.from(handlers).sort();
  }, [tasks]);

  const selectedCount = selectedIds.size;

  const handleRowClick = useCallback(
    (taskId: string) => {
      navigate(`/tasks/${taskId}`);
    },
    [navigate]
  );

  // ── Render ────────────────────────────────────────────────────────────
  return (
    <div className="flex flex-col h-full">
      {/* Filter bar with live indicator */}
      <FilterBar
        handlerOptions={handlerOptions}
        liveSlot={
          <LiveIndicator
            connected={isConnected}
            paused={isPaused}
            bufferedCount={bufferedCount}
            onTogglePause={handleTogglePause}
            onResume={handleResume}
          />
        }
      />

      {/* Paused banner */}
      {isPaused && bufferedCount > 0 && (
        <div className="flex items-center justify-between px-4 py-2 bg-amber-50 dark:bg-amber-900/20 border-b border-amber-200 dark:border-amber-800">
          <span className="text-sm text-amber-800 dark:text-amber-200">
            Feed paused — {bufferedCount} new task
            {bufferedCount !== 1 ? "s" : ""} waiting.
          </span>
          <button
            type="button"
            onClick={handleResume}
            className="px-3 py-1 text-sm font-medium text-white bg-amber-600 hover:bg-amber-700 rounded-md"
          >
            Resume
          </button>
        </div>
      )}

      {/* Bulk action bar */}
      <BulkActionBar
        selectedCount={selectedCount}
        onClearSelection={clearSelection}
        onBulkTimeout={() => setBulkTimeoutOpen(true)}
      />

      {/* Task table */}
      <div className="flex-1 overflow-auto">
        <table className="w-full text-sm">
          <thead className="sticky top-0 bg-gray-50 dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700">
            <tr>
              <th className="w-8 px-2 py-2 text-left">
                <span className="sr-only">Select</span>
              </th>
              <th className="w-[120px] px-2 py-2 text-left font-medium text-gray-600 dark:text-gray-400">
                Task ID
              </th>
              <th className="w-[180px] px-2 py-2 text-left font-medium text-gray-600 dark:text-gray-400">
                Handler
              </th>
              <th className="w-[220px] px-2 py-2 text-left font-medium text-gray-600 dark:text-gray-400">
                Event Type
              </th>
              <th className="w-[140px] px-2 py-2 text-left font-medium text-gray-600 dark:text-gray-400">
                Status
              </th>
              <th className="w-[90px] px-2 py-2 text-left font-medium text-gray-600 dark:text-gray-400">
                Elapsed
              </th>
              <th className="w-[120px] px-2 py-2 text-left font-medium text-gray-600 dark:text-gray-400">
                Decision
              </th>
              <th className="w-[90px] px-2 py-2 text-right font-medium text-gray-600 dark:text-gray-400">
                Actions
              </th>
            </tr>
          </thead>
          <tbody>
            {tasks.length === 0 && (
              <tr>
                <td colSpan={8} className="px-4 py-8 text-center text-gray-500 dark:text-gray-400">
                  No tasks found.
                </td>
              </tr>
            )}
            {tasks.map((task) => (
              <tr
                key={task.id}
                className="border-b border-gray-100 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-800/50 cursor-pointer"
                onClick={(e) => {
                  // Don't navigate if clicking action buttons or checkbox
                  const target = e.target as HTMLElement;
                  if (
                    target.closest("button") ||
                    target.closest("input[type='checkbox']")
                  ) {
                    return;
                  }
                  handleRowClick(task.id);
                }}
              >
                {/* Checkbox */}
                <td className="px-2 py-2" onClick={(e) => e.stopPropagation()}>
                  <input
                    type="checkbox"
                    disabled={!canTimeout(task.status)}
                    checked={selectedIds.has(task.id)}
                    onChange={() => toggleSelect(task.id)}
                    className="rounded border-gray-300 dark:border-gray-600"
                    aria-label={`Select task ${shortId(task.id)}`}
                  />
                </td>

                {/* Task ID */}
                <td className="px-2 py-2 font-mono text-xs">
                  <button
                    type="button"
                    className="hover:text-blue-600 dark:hover:text-blue-400 cursor-pointer border-none bg-transparent p-0 font-mono text-xs"
                    title={task.id}
                    onClick={(e) => {
                      e.stopPropagation();
                      navigator.clipboard.writeText(task.id).catch(() => {});
                    }}
                  >
                    {shortId(task.id)}
                  </button>
                </td>

                {/* Handler */}
                <td
                  className="px-2 py-2 truncate max-w-[180px]"
                  title={task.handler}
                >
                  <button
                    type="button"
                    className="hover:text-blue-600 dark:hover:text-blue-400 cursor-pointer border-none bg-transparent p-0 text-sm"
                    onClick={(e) => {
                      e.stopPropagation();
                      const params = new URLSearchParams(searchParams);
                      params.set("handler", task.handler);
                      const newUrl = `/tasks?${params.toString()}`;
                      navigate(newUrl);
                    }}
                  >
                    {task.handler}
                  </button>
                </td>

                {/* Event Type */}
                <td
                  className="px-2 py-2 truncate max-w-[220px]"
                  title={task.event_type}
                >
                  <span className="block truncate">{task.event_type}</span>
                </td>

                {/* Status */}
                <td className="px-2 py-2">
                  <StatusBadge status={task.status} />
                </td>

                {/* Elapsed / Duration */}
                <td className="px-2 py-2">
                  <ElapsedTimer
                    receivedAt={task.received_at}
                    isProcessing={task.status === "Processing"}
                    durationMs={task.duration_ms}
                  />
                </td>

                {/* Decision */}
                <td className="px-2 py-2 text-xs">
                  {task.status === "Completed" &&
                  task.schema_output &&
                  typeof task.schema_output === "object" &&
                  "decision" in task.schema_output
                    ? String(task.schema_output.decision)
                    : "—"}
                </td>

                {/* Actions */}
                <td className="px-2 py-2 text-right">
                  <div className="flex gap-1 justify-end">
                    {canTimeout(task.status) && (
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          handleSingleTimeout(task);
                        }}
                        className="px-2 py-0.5 text-xs font-medium text-gray-700 bg-gray-100 hover:bg-gray-200 dark:text-gray-200 dark:bg-gray-700 dark:hover:bg-gray-600 rounded border-none cursor-pointer"
                      >
                        Timeout
                      </button>
                    )}
                    {canRetry(task.status) && (
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          handleRetry(task);
                        }}
                        className="px-2 py-0.5 text-xs font-medium text-blue-700 bg-blue-100 hover:bg-blue-200 dark:text-blue-200 dark:bg-blue-800 dark:hover:bg-blue-700 rounded border-none cursor-pointer"
                      >
                        Retry
                      </button>
                    )}
                    {task.status === "Retried" && (
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          navigate(`/tasks/${task.id}/lineage`);
                        }}
                        className="px-2 py-0.5 text-xs font-medium text-gray-700 bg-gray-100 hover:bg-gray-200 dark:text-gray-200 dark:bg-gray-700 dark:hover:bg-gray-600 rounded border-none cursor-pointer"
                      >
                        View chain
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>

        {/* Infinite scroll trigger */}
        {nextCursor && (
          <div className="flex justify-center py-4">
            <button
              type="button"
              onClick={loadMore}
              disabled={loadingOlder}
              className="px-4 py-2 text-sm font-medium text-gray-700 bg-gray-100 hover:bg-gray-200 dark:text-gray-200 dark:bg-gray-700 dark:hover:bg-gray-600 rounded-md disabled:opacity-50 border-none cursor-pointer"
            >
              {loadingOlder ? "Loading..." : "Load older tasks"}
            </button>
          </div>
        )}
      </div>

      {/* Bulk Timeout Confirmation Modal */}
      <ConfirmModal
        open={bulkTimeoutOpen}
        onOpenChange={setBulkTimeoutOpen}
        title={`Mark ${selectedCount} task${selectedCount !== 1 ? "s" : ""} as Timeout?`}
        description="This cannot be undone automatically. Timed-out tasks can be retried."
        confirmLabel="Confirm"
        variant="danger"
        onConfirm={executeBulkTimeout}
      />

      {/* Single Timeout Confirmation Modal */}
      {actionTask && (
        <ConfirmModal
          open={singleTimeoutOpen}
          onOpenChange={setSingleTimeoutOpen}
          title={`Mark task ${shortId(actionTask.id)}... as Timeout?`}
          description="This cannot be undone automatically. Timed-out tasks can be retried."
          confirmLabel="Confirm"
          variant="danger"
          onConfirm={() => executeTimeout(actionTask.id)}
        />
      )}

      {/* Retry Confirmation Modal */}
      {actionTask && (
        <ConfirmModal
          open={retryOpen}
          onOpenChange={setRetryOpen}
          title={`Retry task ${shortId(actionTask.id)}...?`}
          details={[
            { label: "Status", value: actionTask.status },
            { label: "Handler", value: actionTask.handler },
          ]}
          description="The original event will be re-published to NATS. A new task will be created."
          confirmLabel="Confirm"
          onConfirm={() => {
            const baseUrl =
              process.env.TASK_SERVICE_URL ?? "http://localhost:8080";
            fetch(`${baseUrl}/tasks/${actionTask.id}/retry`, {
              method: "POST",
              headers: { "Content-Type": "application/json" },
            }).catch(console.error);
            revalidator.revalidate();
          }}
        />
      )}
    </div>
  );
}
