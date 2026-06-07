import { useState } from "react";
import { Link } from "react-router";
import type { Route } from "./+types/tasks.$id";
import { fetchTask } from "~/lib/api.server";
import type { components } from "~/types/generated/task-service";
import { StatusBadge } from "~/components/status-badge";
import { CollapsibleJson } from "~/components/collapsible-json";

type ActionResult = components["schemas"]["ActionResult"];
type TaskError = components["schemas"]["TaskError"];

export function meta({ data }: Route.MetaArgs) {
  if (!data?.task) {
    return [{ title: "Task not found — KAPE Dashboard" }];
  }
  return [{ title: `Task ${data.task.id.slice(0, 8)} — KAPE Dashboard` }];
}

export async function loader({ params }: Route.LoaderArgs) {
  const task = await fetchTask(params.id);
  const traceUrlTemplate = process.env.TRACE_URL_TEMPLATE ?? "";
  return { task, traceUrlTemplate };
}

export default function TaskDetail({ loaderData }: Route.ComponentProps) {
  const { task, traceUrlTemplate } = loaderData;

  return (
    <div className="mx-auto max-w-7xl px-4 py-6">
      {/* Header */}
      <div className="mb-6">
        <Link
          to="/tasks"
          className="mb-3 inline-flex items-center gap-1 text-sm text-gray-500 hover:text-gray-700"
        >
          <svg className="h-4 w-4" viewBox="0 0 16 16" fill="currentColor">
            <path
              fillRule="evenodd"
              d="M10.354 3.646a.5.5 0 010 .708L6.707 8l3.647 3.646a.5.5 0 01-.708.708l-4-4a.5.5 0 010-.708l4-4a.5.5 0 01.708 0z"
              clipRule="evenodd"
            />
          </svg>
          Back to Tasks
        </Link>

        <div className="flex items-center justify-between">
          <div>
            <h1 className="flex items-center gap-3 text-xl font-semibold text-gray-900">
              <span>Task</span>
              <code className="rounded bg-gray-100 px-2 py-0.5 font-mono text-lg text-gray-700">
                {task.id}
              </code>
              <StatusBadge status={task.status} />
            </h1>
            <dl className="mt-3 flex flex-wrap gap-x-6 gap-y-1 text-sm text-gray-600">
              <div className="flex items-center gap-1">
                <dt className="text-gray-400">Handler:</dt>
                <dd className="font-medium text-gray-700">{task.handler}</dd>
              </div>
              <div className="flex items-center gap-1">
                <dt className="text-gray-400">Namespace:</dt>
                <dd className="font-medium text-gray-700">{task.namespace}</dd>
              </div>
              <div className="flex items-center gap-1">
                <dt className="text-gray-400">Received:</dt>
                <dd>
                  {new Date(task.received_at)
                    .toISOString()
                    .replace("T", " ")
                    .slice(0, 19)}{" "}
                  UTC
                </dd>
              </div>
              {task.duration_ms != null && (
                <div className="flex items-center gap-1">
                  <dt className="text-gray-400">Duration:</dt>
                  <dd>{formatDuration(task.duration_ms)}</dd>
                </div>
              )}
              <div className="flex items-center gap-1">
                <dt className="text-gray-400">Dry run:</dt>
                <dd className="font-medium">
                  {task.dry_run ? "true" : "false"}
                </dd>
              </div>
            </dl>
          </div>
        </div>
      </div>

      {/* Four info panels in 2x2 grid */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {/* Panel 1 — Triggering Event */}
        <Panel title="Triggering Event">
          <KV label="Event Type" value={task.event_type} />
          <KV label="Event Source" value={task.event_source} />
          <KV label="Event ID" value={task.event_id} mono />
          <div className="mt-4">
            <CollapsibleJson data={task.event_raw} label="Raw Event" />
          </div>
        </Panel>

        {/* Panel 2 — LLM Decision */}
        <Panel title="LLM Decision">
          {task.schema_output ? (
            <div className="space-y-3">
              {Object.entries(task.schema_output).map(([key, value]) => (
                <KV
                  key={key}
                  label={formatKey(key)}
                  value={formatValue(value)}
                />
              ))}
            </div>
          ) : (
            <span className="text-gray-400 italic">—</span>
          )}
        </Panel>

        {/* Panel 3 — Actions */}
        <Panel title="Actions">
          {task.actions && task.actions.length > 0 ? (
            <div className="space-y-3">
              {task.actions.map((action, i) => (
                <ActionResultRow key={i} action={action} />
              ))}
            </div>
          ) : (
            <p className="text-sm text-gray-400 italic">No actions recorded</p>
          )}
        </Panel>

        {/* Panel 4 — Error */}
        {task.error && (
          <Panel title="Error">
            <ErrorDisplay error={task.error} />
          </Panel>
        )}
      </div>

      {/* Footer */}
      <div className="mt-8 border-t border-gray-200 pt-4 text-sm text-gray-600">
        <TraceLink
          traceId={task.otel_trace_id ?? null}
          template={traceUrlTemplate}
        />

        {task.retry_of && (
          <div className="mt-2">
            <span className="text-gray-400">Retry of: </span>
            <code className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs">
              {task.retry_of}
            </code>
            <Link
              to={`/tasks/${task.retry_of}/lineage`}
              className="ml-2 text-blue-600 hover:underline"
            >
              View chain
            </Link>
          </div>
        )}
      </div>
    </div>
  );
}

/* ─── Sub-components ─── */

function Panel({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
      <h2 className="mb-4 text-sm font-semibold uppercase tracking-wide text-gray-500">
        {title}
      </h2>
      {children}
    </div>
  );
}

function KV({
  label,
  value,
  mono = false,
}: {
  label: string;
  value: string | null | undefined;
  mono?: boolean;
}) {
  if (value == null) {
    return (
      <div className="flex gap-2 py-1 text-sm">
        <span className="w-36 shrink-0 text-gray-400">{label}</span>
        <span className="text-gray-400 italic">—</span>
      </div>
    );
  }

  return (
    <div className="flex gap-2 py-1 text-sm">
      <span className="w-36 shrink-0 text-gray-400">{label}</span>
      <span className={mono ? "font-mono text-gray-700" : "text-gray-700"}>
        {value}
      </span>
    </div>
  );
}

function ActionResultRow({ action }: { action: ActionResult }) {
  const dotColor =
    action.status === "Completed"
      ? "bg-green-500"
      : action.status === "Failed"
        ? "bg-red-500"
        : "bg-gray-400";

  return (
    <div>
      <div className="flex items-center gap-2">
        <span className={`inline-block h-2 w-2 flex-shrink-0 rounded-full ${dotColor}`} />
        <span className="text-sm font-medium text-gray-700">
          {action.status}
        </span>
        <code className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs text-gray-600">
          {action.name}
        </code>
        <span className="text-xs text-gray-400">{action.type}</span>
        {action.dry_run && (
          <span className="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-500">
            dry run
          </span>
        )}
      </div>
      {action.error && (
        <p className="ml-4 mt-1 text-xs text-red-600">{action.error}</p>
      )}
    </div>
  );
}

function ErrorDisplay({ error }: { error: TaskError }) {
  return (
    <div className="space-y-3">
      <KV label="Type" value={error.type} />
      <KV label="Detail" value={error.detail} />
      {error.schema != null && <KV label="Schema" value={error.schema} />}
      {error.raw != null && <KV label="Raw" value={error.raw} mono />}
      {error.traceback != null && (
        <TracebackToggle traceback={error.traceback} />
      )}
    </div>
  );
}

function TracebackToggle({ traceback }: { traceback: string }) {
  const [open, setOpen] = useState(false);

  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex items-center gap-1 text-sm font-medium text-red-600 hover:text-red-700"
      >
        <svg
          className={`h-3 w-3 transition-transform ${open ? "rotate-90" : ""}`}
          viewBox="0 0 16 16"
          fill="currentColor"
        >
          <path d="M6 4l4 4-4 4V4z" />
        </svg>
        Show traceback
      </button>
      {open && (
        <pre className="mt-2 max-h-96 overflow-auto rounded bg-gray-900 p-3 text-xs text-red-300 font-mono whitespace-pre-wrap">
          {traceback}
        </pre>
      )}
    </div>
  );
}

function TraceLink({
  traceId,
  template,
}: {
  traceId: string | null;
  template: string;
}) {
  if (!template || !traceId) {
    return (
      <div className="flex items-center gap-1">
        <span className="text-gray-400">Trace unavailable</span>
        <span
          className="cursor-help text-xs text-gray-300"
          title="Pod may have crashed before tracer was initialised."
        >
          ⓘ
        </span>
      </div>
    );
  }

  const traceUrl = template.replace("{trace_id}", traceId);

  return (
    <div className="flex items-center gap-2">
      <span className="text-gray-400">OTEL Trace:</span>
      <code className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs text-gray-600">
        {traceId.slice(0, 8)}&hellip;
      </code>
      <a
        href={traceUrl}
        target="_blank"
        rel="noopener noreferrer"
        className="text-blue-600 hover:underline"
      >
        Open trace ↗
      </a>
    </div>
  );
}

/* ─── Helpers ─── */

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const seconds = ms / 1000;
  if (seconds < 60) return `${seconds.toFixed(1)}s`;
  const minutes = Math.floor(seconds / 60);
  const remainingSecs = seconds % 60;
  return `${minutes}m ${remainingSecs.toFixed(0)}s`;
}

function formatKey(key: string): string {
  return key
    .replace(/_/g, " ")
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

function formatValue(value: unknown): string | null | undefined {
  if (value === null || value === undefined) return "—";
  if (typeof value === "boolean") return String(value);
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}
