# Phase 9.1 — TypeScript Type Generation

**Status:** pending
**Phase:** 09-dashboard
**Milestone:** M5
**Specs:** 0009, 0008

## Goal

Set up `openapi-typescript` codegen to auto-generate TypeScript types from `task-service/openapi/openapi.yaml` into `dashboard/app/types/api.ts`. Add a `gen:types` npm script so types stay in sync with the API.

## Work

- Add `openapi-typescript` to `dashboard/package.json` devDependencies
- Add script: `"gen:types": "openapi-typescript ../../task-service/openapi/openapi.yaml -o app/types/api.ts"`
- Run codegen; commit generated `dashboard/app/types/api.ts`
- Add `dashboard/app/types/api.ts` to `.gitignore` or commit it (commit preferred — reviewers can see API shape)

## Acceptance Criteria

- `cd dashboard && npm run gen:types` produces `dashboard/app/types/api.ts` with no errors
- Generated file contains TypeScript types for `Task`, `Handler`, and all API response shapes from `openapi.yaml`

## Key Files

- `dashboard/package.json` (modified)
- `dashboard/app/types/api.ts` (generated/committed)
