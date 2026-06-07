import { useState, useEffect } from "react";
import { formatElapsed, formatDuration } from "~/lib/utils";

interface ElapsedTimerProps {
  receivedAt: string;
  isProcessing: boolean;
  durationMs?: number | null;
}

export function ElapsedTimer({
  receivedAt,
  isProcessing,
  durationMs,
}: ElapsedTimerProps) {
  // tick forces re-render every second for live elapsed time
  const [, setTick] = useState(0);

  useEffect(() => {
    if (!isProcessing) return;
    const interval = setInterval(() => {
      setTick((t) => t + 1);
    }, 1000);
    return () => clearInterval(interval);
  }, [isProcessing]);

  if (isProcessing) {
    // tick is used to force re-render; formatElapsed reads Date.now()
    return (
      <span className="font-mono text-xs tabular-nums">
        {formatElapsed(receivedAt)}
      </span>
    );
  }

  return (
    <span className="font-mono text-xs tabular-nums">
      {formatDuration(durationMs)}
    </span>
  );
}
