import { Link } from "react-router";
import type { Route } from "./+types/tasks.$id.lineage";
import { fetchTaskLineage } from "~/lib/api.server";

import { StatusBadge } from "~/components/status-badge";


export function meta({ data }: Route.MetaArgs) {
  if (!data?.chain || data.chain.length === 0) {
    return [{ title: "Lineage not found — KAPE Dashboard" }];
  }
  const rootId = data.chain[0].id.slice(0, 8);
  return [{ title: `Lineage ${rootId}... — KAPE Dashboard` }];
}

export async function loader({ params }: Route.LoaderArgs) {
  const chain = await fetchTaskLineage(params.id);
  return { chain, currentId: params.id };
}

export default function LineageChain({ loaderData }: Route.ComponentProps) {
  const { chain, currentId } = loaderData;

  return (
    <div className="mx-auto max-w-7xl px-4 py-6">
      {/* Header */}
      <div className="mb-6">
        <Link
          to={`/tasks/${currentId}`}
          className="mb-3 inline-flex items-center gap-1 text-sm text-gray-500 hover:text-gray-700"
        >
          <svg className="h-4 w-4" viewBox="0 0 16 16" fill="currentColor">
            <path
              fillRule="evenodd"
              d="M10.354 3.646a.5.5 0 010 .708L6.707 8l3.647 3.646a.5.5 0 01-.708.708l-4-4a.5.5 0 010-.708l4-4a.5.5 0 01.708 0z"
              clipRule="evenodd"
            />
          </svg>
          Back to Task {currentId.slice(0, 8)}…
        </Link>

        <h1 className="text-xl font-semibold text-gray-900">Retry Lineage</h1>
      </div>

      {/* Horizontal chain summary */}
      <div className="mb-8 rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="font-medium text-gray-500">Lineage:</span>
          {chain.map((task, i) => (
            <span key={task.id} className="flex items-center gap-2">
              {i > 0 && (
                <svg
                  className="h-4 w-4 text-gray-300"
                  viewBox="0 0 16 16"
                  fill="currentColor"
                >
                  <path
                    fillRule="evenodd"
                    d="M6.646 3.646a.5.5 0 01.708 0l3.647 3.647a1 1 0 010 1.414l-3.647 3.647a.5.5 0 01-.708-.708L10.293 8 6.646 4.354a.5.5 0 010-.708z"
                    clipRule="evenodd"
                  />
                </svg>
              )}
              <code
                className={`rounded px-1.5 py-0.5 font-mono text-xs ${
                  task.id === currentId
                    ? "bg-blue-100 text-blue-800 ring-1 ring-blue-300"
                    : "bg-gray-100 text-gray-600"
                }`}
              >
                {task.id.slice(0, 8)}…
              </code>
            </span>
          ))}
          <span className="ml-2 text-gray-400">
            ({chain.length} execution{chain.length !== 1 ? "s" : ""})
          </span>
        </div>
      </div>

      {/* Execution table */}
      <div className="overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm">
        <table className="w-full text-left text-sm">
          <thead className="border-b border-gray-200 bg-gray-50">
            <tr>
              <th className="px-4 py-3 font-medium text-gray-500">#</th>
              <th className="px-4 py-3 font-medium text-gray-500">Task ID</th>
              <th className="px-4 py-3 font-medium text-gray-500">Status</th>
              <th className="px-4 py-3 font-medium text-gray-500">Received</th>
              <th className="px-4 py-3 font-medium text-gray-500">Duration</th>
              <th className="px-4 py-3 font-medium text-gray-500">Handler</th>
              <th className="px-4 py-3 text-right font-medium text-gray-500">
                Action
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {chain.map((task, i) => (
              <tr
                key={task.id}
                className={
                  task.id === currentId
                    ? "bg-blue-50/50"
                    : "hover:bg-gray-50"
                }
              >
                <td className="px-4 py-3 text-gray-400">
                  {task.id === currentId && (
                    <span className="mr-1.5 inline-block h-1.5 w-1.5 rounded-full bg-blue-500" />
                  )}
                  #{i + 1}
                </td>
                <td className="px-4 py-3">
                  <code
                    className={`rounded px-1.5 py-0.5 font-mono text-xs ${
                      task.id === currentId
                        ? "bg-blue-100 text-blue-800"
                        : "bg-gray-100 text-gray-600"
                    }`}
                  >
                    {task.id.slice(0, 8)}…
                  </code>
                </td>
                <td className="px-4 py-3">
                  <StatusBadge status={task.status} />
                </td>
                <td className="px-4 py-3 text-gray-600">
                  {new Date(task.received_at)
                    .toISOString()
                    .replace("T", " ")
                    .slice(0, 19)}
                </td>
                <td className="px-4 py-3 text-gray-600">
                  {task.duration_ms != null
                    ? formatDuration(task.duration_ms)
                    : "—"}
                </td>
                <td className="px-4 py-3 text-gray-600">{task.handler}</td>
                <td className="px-4 py-3 text-right">
                  <Link
                    to={`/tasks/${task.id}`}
                    className="text-blue-600 hover:underline"
                  >
                    View detail
                  </Link>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
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
