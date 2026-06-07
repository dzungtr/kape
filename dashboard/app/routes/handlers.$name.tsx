import { useState } from "react";
import { Link, useLoaderData } from "react-router";
import { fetchHandlerDetail } from "~/mocks/handlers";
import type { HandlerDetailData } from "~/mocks/handlers";
import type { Route } from "./+types/handlers.$name";

// ---------------------------------------------------------------------------
// Loader
// ---------------------------------------------------------------------------

export async function loader({ params }: Route.LoaderArgs): Promise<HandlerDetailData> {
  // Default time range: 24h
  const since = new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString();
  return fetchHandlerDetail(params.name!, since);
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const TIME_RANGES = [
  { label: "Last 1h", hours: 1 },
  { label: "6h", hours: 6 },
  { label: "24h", hours: 24 },
  { label: "7d", hours: 168 },
] as const;

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
// Decision distribution bar chart
// ---------------------------------------------------------------------------

interface DecisionBarProps {
  distribution: Record<string, number>;
  nonCompleted: Record<string, number>;
}

const BAR_COLORS = [
  "bg-blue-500",
  "bg-emerald-500",
  "bg-amber-500",
  "bg-purple-500",
  "bg-rose-500",
  "bg-cyan-500",
];

function DecisionDistributionChart({ distribution, nonCompleted }: DecisionBarProps) {
  const entries = Object.entries(distribution);
  const completedTotal = entries.reduce((sum, [, c]) => sum + c, 0);
  const nonCompletedTotal = Object.values(nonCompleted).reduce((sum, c) => sum + c, 0);
  const grandTotal = completedTotal + nonCompletedTotal;

  if (completedTotal === 0) {
    return (
      <p className="text-gray-400 text-sm italic">No completed tasks in this window.</p>
    );
  }

  return (
    <div className="space-y-4">
      {/* Stacked bar */}
      <div className="flex h-8 rounded overflow-hidden bg-gray-100">
        {entries.map(([decision, count], idx) => {
          const pct = (count / grandTotal) * 100;
          return (
            <div
              key={decision}
              className={`${BAR_COLORS[idx % BAR_COLORS.length]} flex items-center justify-center text-xs text-white font-medium`}
              style={{ width: `${pct}%`, minWidth: count > 0 ? "2rem" : 0 }}
              title={`${decision}: ${count} (${(count / completedTotal * 100).toFixed(1)}%)`}
            >
              {pct > 8 ? count : ""}
            </div>
          );
        })}
      </div>

      {/* Breakdown table */}
      <div className="overflow-x-auto">
        <table className="min-w-full text-sm">
          <thead>
            <tr className="border-b border-gray-200">
              <th className="px-3 py-1.5 text-left text-xs font-medium text-gray-500 uppercase">
                Decision
              </th>
              <th className="px-3 py-1.5 text-right text-xs font-medium text-gray-500 uppercase">
                Count
              </th>
              <th className="px-3 py-1.5 text-right text-xs font-medium text-gray-500 uppercase">
                % of Completed
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {entries.map(([decision, count], idx) => {
              const pct = ((count / completedTotal) * 100).toFixed(1);
              return (
                <tr key={decision}>
                  <td className="px-3 py-1.5">
                    <span className="inline-flex items-center gap-1.5">
                      <span
                        className={`inline-block w-3 h-3 rounded ${BAR_COLORS[idx % BAR_COLORS.length]}`}
                      />
                      {decision}
                    </span>
                  </td>
                  <td className="px-3 py-1.5 text-right text-gray-900">{count}</td>
                  <td className="px-3 py-1.5 text-right text-gray-500">{pct}%</td>
                </tr>
              );
            })}
            <tr>
              <td className="px-3 py-1.5 font-medium text-gray-600">Total Completed</td>
              <td className="px-3 py-1.5 text-right font-medium text-gray-900">{completedTotal}</td>
              <td className="px-3 py-1.5 text-right text-gray-500">100%</td>
            </tr>
          </tbody>
        </table>
      </div>

      {/* Non-completed breakdown */}
      {nonCompletedTotal > 0 && (
        <div className="mt-4 p-3 bg-gray-50 rounded-lg">
          <p className="text-sm font-medium text-gray-700">
            Non-completed tasks in window: {nonCompletedTotal}
          </p>
          <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-sm text-gray-500">
            {Object.entries(nonCompleted).map(([status, count]) => (
              <span key={status}>
                {status}: <span className="font-medium text-gray-700">{count}</span>
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function HandlerDetail() {
  const data = useLoaderData<typeof loader>();
  const [selectedRange, setSelectedRange] = useState<string>("24h");

  return (
    <div className="p-6 space-y-8">
      {/* Summary strip */}
      <div>
        <div className="flex items-center justify-between flex-wrap gap-2">
          <h1 className="text-2xl font-bold text-gray-900">{data.handler}</h1>
          <Link
            to={`/tasks?handler=${encodeURIComponent(data.handler)}`}
            className="text-sm text-blue-600 hover:underline"
          >
            View Tasks
          </Link>
        </div>
        <div className="mt-2 flex flex-wrap gap-x-6 gap-y-1 text-sm text-gray-600">
          <span>
            <span className="font-medium text-gray-500">Namespace:</span> {data.namespace}
          </span>
          <span>
            <span className="font-medium text-gray-500">Last task:</span>{" "}
            <span title={new Date(data.last_task_at).toLocaleString()}>
              {relativeTime(data.last_task_at)}
            </span>
          </span>
          <span>
            <span className="font-medium text-gray-500">Tasks today:</span>{" "}
            {data.tasks_today.toLocaleString()}
          </span>
          <span>
            <span className="font-medium text-gray-500">Failures today:</span>{" "}
            {data.failures_today > 0 ? (
              <span className="text-red-600 font-medium">{data.failures_today}</span>
            ) : (
              data.failures_today
            )}
          </span>
        </div>
      </div>

      {/* Decision distribution */}
      <section>
        <h2 className="text-lg font-semibold text-gray-900 mb-3">Decision Distribution</h2>

        {/* Time range selector */}
        <div className="flex gap-1 mb-4" role="group" aria-label="Time range">
          {TIME_RANGES.map(({ label }) => (
            <button
              key={label}
              type="button"
              className={`px-3 py-1 text-sm rounded-md border ${
                selectedRange === label
                  ? "bg-blue-600 text-white border-blue-600"
                  : "bg-white text-gray-700 border-gray-300 hover:bg-gray-50"
              }`}
              onClick={() => setSelectedRange(label)}
            >
              {label}
            </button>
          ))}
        </div>

        <DecisionDistributionChart
          distribution={data.decision_distribution.distribution}
          nonCompleted={data.non_completed}
        />
      </section>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Error boundary — unknown handler
// ---------------------------------------------------------------------------

export function ErrorBoundary({ error: _error }: Route.ErrorBoundaryProps) {
  return (
    <div className="p-6">
      <h1 className="text-xl font-bold text-red-600">Handler not found</h1>
      <p className="mt-2 text-gray-600">
        The requested handler does not exist or has no task records.
      </p>
      <Link to="/handlers" className="mt-4 inline-block text-blue-600 hover:underline">
        ← Back to Handlers
      </Link>
    </div>
  );
}
