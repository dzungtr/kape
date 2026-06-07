import type { components } from "~/types/generated/task-service";

type TaskStatus = components["schemas"]["TaskStatus"];

export function shortId(id: string): string {
  return id.slice(0, 8);
}

export function formatDuration(ms: number | null | undefined): string {
  if (ms == null) return "—";
  if (ms < 1000) return `${ms}ms`;
  const seconds = ms / 1000;
  if (seconds < 60) return `${seconds.toFixed(1)}s`;
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = Math.floor(seconds % 60);
  return `${minutes}m ${remainingSeconds}s`;
}

export function formatElapsed(receivedAt: string): string {
  const now = Date.now();
  const received = new Date(receivedAt).getTime();
  const elapsed = Math.max(0, now - received);
  return formatDuration(elapsed);
}

export function statusBadgeColor(status: TaskStatus): string {
  switch (status) {
    case "Processing":
      return "bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200";
    case "Completed":
      return "bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200";
    case "ActionError":
      return "bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200";
    case "Failed":
    case "SchemaValidationFailed":
    case "UnprocessableEvent":
      return "bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200";
    case "Timeout":
      return "bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-200";
    case "Retried":
      return "bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-200";
    case "PendingApproval":
      return "bg-purple-100 text-purple-800 dark:bg-purple-900 dark:text-purple-200";
    default:
      return "bg-gray-100 text-gray-800";
  }
}

export function canTimeout(status: TaskStatus): boolean {
  return status === "Processing";
}

export function canRetry(status: TaskStatus): boolean {
  return (
    status === "ActionError" ||
    status === "Failed" ||
    status === "SchemaValidationFailed" ||
    status === "Timeout"
  );
}

const STATUS_OPTIONS: TaskStatus[] = [
  "Processing",
  "Completed",
  "Failed",
  "SchemaValidationFailed",
  "ActionError",
  "UnprocessableEvent",
  "Timeout",
  "Retried",
  "PendingApproval",
];

export { STATUS_OPTIONS };
export type { TaskStatus };
