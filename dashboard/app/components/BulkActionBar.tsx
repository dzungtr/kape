interface BulkActionBarProps {
  selectedCount: number;
  onClearSelection: () => void;
  onBulkTimeout: () => void;
}

export function BulkActionBar({
  selectedCount,
  onClearSelection,
  onBulkTimeout,
}: BulkActionBarProps) {
  if (selectedCount === 0) return null;

  return (
    <div className="flex items-center gap-3 px-4 py-2 bg-blue-50 dark:bg-blue-900/20 border-b border-blue-200 dark:border-blue-800">
      <span className="text-sm font-medium text-blue-800 dark:text-blue-200">
        {selectedCount} task{selectedCount !== 1 ? "s" : ""} selected
      </span>
      <button
        type="button"
        onClick={onBulkTimeout}
        className="px-3 py-1 text-sm font-medium text-white bg-blue-600 hover:bg-blue-700 rounded-md"
      >
        Mark as Timeout
      </button>
      <button
        type="button"
        onClick={onClearSelection}
        className="px-3 py-1 text-sm font-medium text-blue-700 bg-blue-100 hover:bg-blue-200 dark:text-blue-200 dark:bg-blue-800 dark:hover:bg-blue-700 rounded-md"
      >
        Clear selection
      </button>
    </div>
  );
}
