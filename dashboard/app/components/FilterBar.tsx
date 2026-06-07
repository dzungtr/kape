import { useSearchParams } from "react-router";
import { STATUS_OPTIONS } from "~/lib/utils";

const TIME_RANGES = [
  { label: "15m", value: "15m" },
  { label: "1h", value: "1h" },
  { label: "6h", value: "6h" },
  { label: "24h", value: "24h" },
];

interface FilterBarProps {
  handlerOptions: string[];
  liveSlot?: React.ReactNode;
}

export function FilterBar({ handlerOptions, liveSlot }: FilterBarProps) {
  const [searchParams, setSearchParams] = useSearchParams();

  const handler = searchParams.get("handler") ?? "";
  const status = searchParams.get("status") ?? "";
  const since = searchParams.get("since") ?? "1h";
  const search = searchParams.get("search") ?? "";

  const updateParam = (key: string, value: string) => {
    const next = new URLSearchParams(searchParams);
    if (value) {
      next.set(key, value);
    } else {
      next.delete(key);
    }
    setSearchParams(next, { replace: true });
  };

  return (
    <div className="flex flex-wrap items-center gap-3 p-3 bg-white dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700">
      {/* Handler filter */}
      <select
        value={handler}
        onChange={(e) => updateParam("handler", e.target.value)}
        className="text-sm border border-gray-300 dark:border-gray-600 rounded-md px-2 py-1 bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100"
        aria-label="Filter by handler"
      >
        <option value="">Handler: All</option>
        {handlerOptions.map((h) => (
          <option key={h} value={h}>
            {h}
          </option>
        ))}
      </select>

      {/* Status filter */}
      <select
        value={status}
        onChange={(e) => updateParam("status", e.target.value)}
        className="text-sm border border-gray-300 dark:border-gray-600 rounded-md px-2 py-1 bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100"
        aria-label="Filter by status"
      >
        <option value="">Status: All</option>
        {STATUS_OPTIONS.map((s) => (
          <option key={s} value={s}>
            {s}
          </option>
        ))}
      </select>

      {/* Time range */}
      <select
        value={since}
        onChange={(e) => updateParam("since", e.target.value)}
        className="text-sm border border-gray-300 dark:border-gray-600 rounded-md px-2 py-1 bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100"
        aria-label="Filter by time range"
      >
        {TIME_RANGES.map((t) => (
          <option key={t.value} value={t.value}>
            Last: {t.label}
          </option>
        ))}
      </select>

      {/* Event type search */}
      <input
        type="text"
        value={search}
        onChange={(e) => updateParam("search", e.target.value)}
        placeholder="Search event type..."
        className="text-sm border border-gray-300 dark:border-gray-600 rounded-md px-2 py-1 bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 placeholder-gray-400 dark:placeholder-gray-500 w-48"
        aria-label="Search by event type"
      />

      <div className="flex-1" />

      {/* Live indicator slot */}
      {liveSlot}
    </div>
  );
}
