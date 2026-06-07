import { type RouteConfig, index, route } from "@react-router/dev/routes";

export default [
  index("routes/_index.tsx"),
  route("handlers", "routes/handlers._index.tsx"),
  route("handlers/:name", "routes/handlers.$name.tsx"),
  route("sse/tasks", "routes/sse.tasks.ts"),
  route("tasks", "routes/tasks._index.tsx"),
  route("tasks/:id", "routes/tasks.$id.tsx"),
  route("tasks/:id/lineage", "routes/tasks.$id.lineage.tsx"),
] satisfies RouteConfig;
