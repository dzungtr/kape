import type { components } from "~/types/generated/task-service";

type Task = components["schemas"]["Task"];
type TaskList = components["schemas"]["TaskList"];

function getTaskServiceUrl(): string {
  const url = process.env.TASK_SERVICE_URL;
  if (!url) {
    throw new Error("TASK_SERVICE_URL environment variable is not set");
  }
  return url;
}

async function apiFetch<T>(
  path: string,
  init?: RequestInit
): Promise<T> {
  const baseUrl = getTaskServiceUrl();
  const url = `${baseUrl}${path}`;

  const response = await fetch(url, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...init?.headers,
    },
  });

  if (!response.ok) {
    const errorBody = await response.text();
    throw new Error(
      `Task service error ${response.status} for ${path}: ${errorBody}`
    );
  }

  const contentType = response.headers.get("content-type");
  if (contentType && contentType.includes("application/json")) {
    return response.json() as Promise<T>;
  }

  return undefined as unknown as T;
}

export interface ListTasksParams {
  handler?: string;
  status?: string;
  since?: string;
  sort?: "received_at:asc" | "received_at:desc";
  limit?: number;
  cursor?: string;
}

export async function listTasks(params: ListTasksParams = {}): Promise<TaskList> {
  const searchParams = new URLSearchParams();
  if (params.handler) searchParams.set("handler", params.handler);
  if (params.status) searchParams.set("status", params.status);
  if (params.since) searchParams.set("since", params.since);
  if (params.sort) searchParams.set("sort", params.sort);
  if (params.limit) searchParams.set("limit", String(params.limit));
  if (params.cursor) searchParams.set("cursor", params.cursor);

  const qs = searchParams.toString();
  const path = qs ? `/tasks?${qs}` : "/tasks";
  return apiFetch<TaskList>(path);
}

export type { Task, TaskList };
