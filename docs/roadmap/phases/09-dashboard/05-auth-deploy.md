# Phase 9.5 — Auth + Deploy

**Status:** pending
**Phase:** 09-dashboard
**Milestone:** M5
**Specs:** 0009

## Goal

Enforce GitHub OAuth2 org/team membership via OAuth2 Proxy. Ship production Dockerfile and Kubernetes deployment manifests.

## Work

- `dashboard/Dockerfile`:
  ```dockerfile
  FROM node:22-alpine AS builder
  WORKDIR /app
  COPY package*.json ./
  RUN npm ci
  COPY . .
  RUN npm run build

  FROM node:22-alpine
  WORKDIR /app
  COPY --from=builder /app/build ./build
  COPY --from=builder /app/node_modules ./node_modules
  CMD ["node", "build/server/index.js"]
  ```
- `examples/dashboard/deployment.yaml`: `Deployment` + `ClusterIP` Service in `kape-system`
- `examples/dashboard/oauth2-proxy.yaml`: OAuth2 Proxy `Deployment` + `Service`
  - `--provider=github`
  - `--github-org=<your-org>`
  - `--github-team=<your-team>` (optional)
  - `--upstream=http://dashboard:3000`
- `examples/dashboard/ingress.yaml`: `Ingress` with TLS termination pointing to OAuth2 Proxy service

## Acceptance Criteria

- `docker build -f dashboard/Dockerfile .` succeeds
- Unauthenticated request to dashboard Ingress URL redirected to GitHub OAuth flow
- GitHub account not in org/team → 403
- GitHub account in org/team → dashboard loads

## Key Files

- `dashboard/Dockerfile` (new)
- `examples/dashboard/deployment.yaml` (new)
- `examples/dashboard/oauth2-proxy.yaml` (new)
- `examples/dashboard/ingress.yaml` (new)
