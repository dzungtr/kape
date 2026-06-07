import { Link, useLoaderData } from "react-router";
import { useState } from "react";
import { fetchHandlers } from "~/mocks/handlers";
import type { HandlerAggregate } from "~/mocks/handlers";
import type { Route } from "./+types/handlers._index";

// ---------------------------------------------------------------------------
// Loader
// ---------------------------------------------------------------------------

export async function loader(_args: Route.LoaderArgs): Promise<HandlerAggregate[]> {
  return fetchHandlers();
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type SortKey = keyof HandlerAggregate;
type SortDir = "asc" | "desc";

function sortHandlers(
  handlers: HandlerAggregate[],
  key: SortKey,
  dir: SortDir,
): HandlerAggregate[] {
  return [...handlers].sort((a, b) => {
    const av = a[key];
    const bv = b[key];
    const cmp =
      typeof av === "number" && typeof bv === "number"
        ? av - bv
        : String(av).localeCompare(String(bv));
    return dir === "asc" ? cmp : -cmp;
  });
}

function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

// ---------------------------------------------------------------------------
// Sortable table header
// ---------------------------------------------------------------------------

interface ThProps {
  label: string;
  sortKey: SortKey;
  currentKey: SortKey;
  dir: SortDir;
  onSort: (key: SortKey) => void;
}

function SortableTh({ label, sortKey, currentKey, dir, onSort }: ThProps) {
  const active = sortKey === currentKey;
  return (
    <th
      className="px-3 py-2 text-left text-xs font-medium text-gray-500 uppercase tracking-wider cursor-pointer select-none hover:text-gray-700"
      onClick={() => onSort(sortKey)}
    >
      <span className="inline-flex items-center gap-1">
        {label}
        {active && <span className="text-gray-400">{dir === "asc" ? "▲" : "▼"}</span>}
      </span>
    </th>
  );
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function HandlersIndex() {
  const data = useLoaderData<typeof loader>();
  const [sortKey, setSortKey] = useState<SortKey>("last_task_at");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  const handleSort = (key: SortKey) => {
    if (key === sortKey) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setSortDir("desc");
    }
  };

  const sorted = sortHandlers(data, sortKey, sortDir);

  return (
    <div className="p-6">
      <h1 className="text-2xl font-bold text-gray-900 mb-4">Handlers</h1>

      <div className="overflow-x-auto bg-white rounded-lg shadow ring-1 ring-gray-200">
        <table className="min-w-full divide-y divide-gray-200 text-sm">
          <thead className="bg-gray-50">
            <tr>
              <SortableTh
                label="Handler"
                sortKey="handler"
                currentKey={sortKey}
                dir={sortDir}
                onSort={handleSort}
              />
              <SortableTh
                label="Namespace"
                sortKey="namespace"
                currentKey={sortKey}
                dir={sortDir}
                onSort={handleSort}
              />
              <SortableTh
                label="Last Task"
                sortKey="last_task_at"
                currentKey={sortKey}
                dir={sortDir}
                onSort={handleSort}
              />
              <SortableTh
                label="Tasks (24h)"
                sortKey="tasks_24h"
                currentKey={sortKey}
                dir={sortDir}
                onSort={handleSort}
              />
              <SortableTh
                label="Failures (24h)"
                sortKey="failures_24h"
                currentKey={sortKey}
                dir={sortDir}
                onSort={handleSort}
              />
              <SortableTh
                label="Processing"
                sortKey="processing_count"
                currentKey={sortKey}
                dir={sortDir}
                onSort={handleSort}
              />
              <th className="px-3 py-2 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Actions
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {sorted.map((h) => (
              <tr key={h.handler} className="hover:bg-gray-50">
                <td className="px-3 py-2 font-medium text-blue-600">
                  <Link to={`/handlers/${encodeURIComponent(h.handler)}`} className="hover:underline">
                    {h.handler}
                  </Link>
                </td>
                <td className="px-3 py-2 text-gray-700">{h.namespace}</td>
                <td className="px-3 py-2 text-gray-500" title={new Date(h.last_task_at).toLocaleString()}>
                  {relativeTime(h.last_task_at)}
                </td>
                <td className="px-3 py-2 text-gray-900">{h.tasks_24h.toLocaleString()}</td>
                <td className="px-3 py-2">
                  {h.failures_24h > 0 ? (
                    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-red-100 text-red-800">
                      {h.failures_24h}
                    </span>
                  ) : (
                    <span className="text-gray-400">{h.failures_24h}</span>
                  )}
                </td>
                <td className="px-3 py-2">
                  {h.processing_count > 0 ? (
                    <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-amber-100 text-amber-800">
                      {h.processing_count}
                    </span>
                  ) : (
                    <span className="text-gray-400">{h.processing_count}</span>
                  )}
                </td>
                <td className="px-3 py-2">
                  <Link
                    to={`/tasks?handler=${encodeURIComponent(h.handler)}`}
                    className="text-blue-600 hover:underline text-xs"
                  >
                    View Tasks
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
