import type { components } from "~/types/generated/task-service";
import { statusBadgeColor } from "~/lib/utils";

type TaskStatus = components["schemas"]["TaskStatus"];

interface StatusBadgeProps {
  status: TaskStatus;
}

export function StatusBadge({ status }: StatusBadgeProps) {
  const colorClasses = statusBadgeColor(status);
  const isProcessing = status === "Processing";
  const isRetried = status === "Retried";

  return (
    <span
      className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${colorClasses} ${
        isProcessing ? "status-pulsing" : ""
      } ${isRetried ? "status-strikethrough" : ""}`}
    >
      {status}
    </span>
  );
}
