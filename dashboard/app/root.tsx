import type { LoaderFunctionArgs } from "react-router";
import {
  Links,
  Meta,
  Outlet,
  Scripts,
  ScrollRestoration,
  useLoaderData,
  NavLink,
} from "react-router";
import "./root.css";

export function loader({ request }: LoaderFunctionArgs) {
  const user = request.headers.get("X-Forwarded-User") ?? "Unknown";
  return { user };
}

export default function Root() {
  const { user } = useLoaderData() as { user: string };

  return (
    <html lang="en" className="h-full">
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <Meta />
        <Links />
      </head>
      <body className="h-full bg-gray-50 dark:bg-gray-900 text-gray-900 dark:text-gray-100">
        <div className="flex h-full flex-col">
          {/* Top nav bar */}
          <header className="flex items-center justify-between px-6 py-3 bg-white dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700 shrink-0">
            <div className="flex items-center gap-6">
              <span className="font-bold text-lg">KAPE Dashboard</span>
              <nav className="flex gap-4">
                <NavLink
                  to="/tasks"
                  className={({ isActive }) =>
                    `text-sm font-medium ${
                      isActive
                        ? "text-blue-600 dark:text-blue-400"
                        : "text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-gray-100"
                    }`
                  }
                >
                  Tasks
                </NavLink>
                <NavLink
                  to="/handlers"
                  className={({ isActive }) =>
                    `text-sm font-medium ${
                      isActive
                        ? "text-blue-600 dark:text-blue-400"
                        : "text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-gray-100"
                    }`
                  }
                >
                  Handlers
                </NavLink>
              </nav>
            </div>
            <div className="text-sm text-gray-500 dark:text-gray-400">
              {user}
            </div>
          </header>

          {/* Page content */}
          <main className="flex-1 overflow-auto">
            <Outlet />
          </main>
        </div>

        <ScrollRestoration />
        <Scripts />
      </body>
    </html>
  );
}
