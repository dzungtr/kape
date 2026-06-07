import { useState } from "react";

interface CollapsibleJsonProps {
  data: Record<string, unknown> | null;
  defaultOpen?: boolean;
  label?: string;
}

export function CollapsibleJson({
  data,
  defaultOpen = false,
  label = "Payload",
}: CollapsibleJsonProps) {
  const [open, setOpen] = useState(defaultOpen);

  if (!data) {
    return <span className="text-gray-400 italic">—</span>;
  }

  const json = JSON.stringify(data, null, 2);

  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex items-center gap-1 text-sm font-medium text-gray-700 hover:text-gray-900"
      >
        <svg
          className={`h-3 w-3 transition-transform ${open ? "rotate-90" : ""}`}
          viewBox="0 0 16 16"
          fill="currentColor"
        >
          <path d="M6 4l4 4-4 4V4z" />
        </svg>
        {label}
        <span className="text-xs text-gray-400 font-normal">
          ({Object.keys(data).length} fields)
        </span>
      </button>
      {open && (
        <pre className="mt-2 max-h-96 overflow-auto rounded bg-gray-900 p-3 text-xs text-green-300 font-mono">
          {json}
        </pre>
      )}
    </div>
  );
}
