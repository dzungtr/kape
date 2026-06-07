import { useCallback } from "react";

interface LiveIndicatorProps {
  connected: boolean;
  paused: boolean;
  bufferedCount: number;
  onTogglePause: () => void;
  onResume: () => void;
}

export function LiveIndicator({
  connected,
  paused,
  bufferedCount,
  onTogglePause,
  onResume,
}: LiveIndicatorProps) {
  const handleClick = useCallback(() => {
    if (paused) {
      onResume();
    } else {
      onTogglePause();
    }
  }, [paused, onResume, onTogglePause]);

  return (
    <div className="flex items-center gap-2">
      {paused && bufferedCount > 0 && (
        <span className="text-xs text-amber-600 dark:text-amber-400">
          {bufferedCount} new
        </span>
      )}
      <button
        type="button"
        onClick={handleClick}
        className="flex items-center gap-1 text-xs font-medium cursor-pointer border-none bg-transparent"
        title={
          paused
            ? "Resume live updates"
            : connected
            ? "Pause live updates"
            : "Reconnecting..."
        }
      >
        <span
          className={`inline-block w-2 h-2 rounded-full ${
            connected && !paused
              ? "bg-green-500"
              : connected && paused
              ? "bg-amber-500"
              : "bg-red-500"
          }`}
        />
        {paused ? "Paused" : connected ? "Live" : "Reconnecting"}
      </button>
    </div>
  );
}
