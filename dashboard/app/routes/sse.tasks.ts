const TASK_SERVICE_URL = process.env.TASK_SERVICE_URL ?? "http://localhost:8080";

export async function loader(): Promise<Response> {
  const upstreamUrl = `${TASK_SERVICE_URL}/tasks/stream`;

  const upstreamResponse = await fetch(upstreamUrl, {
    headers: {
      Accept: "text/event-stream",
    },
  });

  if (!upstreamResponse.ok || !upstreamResponse.body) {
    return new Response("SSE upstream unavailable", { status: 502 });
  }

  return new Response(upstreamResponse.body, {
    status: 200,
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      Connection: "keep-alive",
      "X-Accel-Buffering": "no",
    },
  });
}
