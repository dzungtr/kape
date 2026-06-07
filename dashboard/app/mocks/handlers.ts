import type { components } from "~/types/generated/task-service";

/**
 * Aggregated handler row for the handlers overview table.
 * Derived from task-service task-record aggregates.
 * Per spec 0008 §6.4. Not yet part of the generated OpenAPI types.
 */
export interface HandlerAggregate {
  handler: string;
  namespace: string;
  /** ISO-8601 timestamp — MAX(received_at) */
  last_task_at: string;
  /** COUNT tasks WHERE received_at > now() - 24h */
  tasks_24h: number;
  /** COUNT tasks WHERE status IN (Failed, SchemaValidationFailed, ActionError) AND received_at > now() - 24h */
  failures_24h: number;
  /** COUNT tasks WHERE status = Processing */
  processing_count: number;
}

/**
 * Handler detail data including the summary strip values and the decision
 * distribution plus non-completed task breakdown.
 */
export interface HandlerDetailData {
  handler: string;
  namespace: string;
  /** ISO-8601 */
  last_task_at: string;
  /** COUNT today */
  tasks_today: number;
  /** COUNT today WHERE status IN (Failed, SchemaValidationFailed, ActionError) */
  failures_today: number;
  /** Decision distribution from /tasks/decisions */
  decision_distribution: components["schemas"]["DecisionDistribution"];
  /** Non-completed task counts broken out by status */
  non_completed: Record<string, number>;
}

// ---------------------------------------------------------------------------
// Stub endpoint helpers
// ---------------------------------------------------------------------------

const TASK_SERVICE_BASE = process.env.TASK_SERVICE_URL || "http://localhost:8080";

/** Returns true if the response is a 501 (not implemented). */
function is501(res: Response): boolean {
  return res.status === 501;
}

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

const MOCK_HANDLERS: HandlerAggregate[] = [
  {
    handler: "falco-terminal-shell",
    namespace: "kape-system",
    last_task_at: new Date(Date.now() - 2 * 60 * 1000).toISOString(),
    tasks_24h: 1847,
    failures_24h: 23,
    processing_count: 3,
  },
  {
    handler: "karpenter-disruption-alert",
    namespace: "kape-system",
    last_task_at: new Date(Date.now() - 15 * 60 * 1000).toISOString(),
    tasks_24h: 542,
    failures_24h: 7,
    processing_count: 1,
  },
  {
    handler: "trivy-scan-result",
    namespace: "kape-system",
    last_task_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
    tasks_24h: 312,
    failures_24h: 4,
    processing_count: 0,
  },
  {
    handler: "calico-network-policy",
    namespace: "kape-system",
    last_task_at: new Date(Date.now() - 4 * 60 * 60 * 1000).toISOString(),
    tasks_24h: 89,
    failures_24h: 0,
    processing_count: 0,
  },
  {
    handler: "cert-manager-expiry",
    namespace: "kape-system",
    last_task_at: new Date(Date.now() - 12 * 60 * 60 * 1000).toISOString(),
    tasks_24h: 24,
    failures_24h: 1,
    processing_count: 0,
  },
];

function makeDecisionDistribution(
  handler: string,
  since: string,
): components["schemas"]["DecisionDistribution"] {
  return {
    handler,
    since,
    distribution: {
      ignore: 89,
      investigate: 28,
      "change-required": 6,
    },
  };
}

const MOCK_NON_COMPLETED: Record<string, number> = {
  Failed: 3,
  ActionError: 8,
  Processing: 13,
};

// ---------------------------------------------------------------------------
// Public API — typed against generated types where available,
// HandlerAggregate / HandlerDetailData otherwise.
// ---------------------------------------------------------------------------

/** GET /handlers → HandlerAggregate[] or 501 stub */
export async function fetchHandlers(): Promise<HandlerAggregate[]> {
  const res = await fetch(`${TASK_SERVICE_BASE}/handlers`);
  if (is501(res)) return MOCK_HANDLERS;
  if (!res.ok) throw new Error(`GET /handlers failed: ${res.status}`);
  return res.json();
}

/** GET /tasks/decisions?handler=X&since=Z → DecisionDistribution or 501 stub */
export async function fetchDecisions(
  handler: string,
  since: string,
): Promise<components["schemas"]["DecisionDistribution"]> {
  const url = `${TASK_SERVICE_BASE}/tasks/decisions?handler=${encodeURIComponent(handler)}&since=${encodeURIComponent(since)}`;
  const res = await fetch(url);
  if (is501(res)) return makeDecisionDistribution(handler, since);
  if (!res.ok)
    throw new Error(`GET /tasks/decisions failed: ${res.status}`);
  return res.json();
}

/**
 * Fetch full handler detail — combined aggregate (from /handlers mock) +
 * decision distribution.
 */
export async function fetchHandlerDetail(
  handler: string,
  since: string,
): Promise<HandlerDetailData> {
  const [handlers, decisions] = await Promise.all([
    fetchHandlers(),
    fetchDecisions(handler, since),
  ]);

  const match = handlers.find((h) => h.handler === handler);
  if (!match) {
    throw new Response("Handler not found", { status: 404 });
  }

  // Derive today counts from the 24h mock data scaled down for demo.
  return {
    handler: match.handler,
    namespace: match.namespace,
    last_task_at: match.last_task_at,
    tasks_today: match.tasks_24h,
    failures_today: match.failures_24h,
    decision_distribution: decisions,
    non_completed: MOCK_NON_COMPLETED,
  };
}
