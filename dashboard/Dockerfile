FROM oven/bun:1.3.12-alpine AS builder
WORKDIR /app
COPY package.json bun.lock ./
COPY dashboard/package.json ./dashboard/
COPY dashboard/ ./dashboard/
RUN cd dashboard && bun install --frozen-lockfile
RUN cd dashboard && node_modules/.bin/react-router build

FROM oven/bun:1.3.12-alpine
WORKDIR /app
COPY --from=builder /app/dashboard/build ./build
COPY --from=builder /app/dashboard/node_modules ./node_modules
COPY --from=builder /app/dashboard/package.json ./
EXPOSE 3000
CMD ["bun", "build/server/index.js"]
