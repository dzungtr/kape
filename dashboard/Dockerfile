FROM oven/bun:1.3.12-alpine AS builder
WORKDIR /app
COPY dashboard/package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY dashboard/ .
RUN bun run build

FROM oven/bun:1.3.12-alpine
WORKDIR /app
COPY --from=builder /app/build ./build
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/package.json ./
EXPOSE 3000
CMD ["bun", "build/server/index.js"]
