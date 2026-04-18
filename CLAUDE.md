# kape-io Project Instructions

## PR Checklist

Before creating any PR, run SBOM generation for all Go modules and post a summary comment:

1. Run `snyk_sbom_scan` MCP tool on each Go module with format `cyclonedx1.4+json`:
   - Path: `./adapters`
   - Path: `./operator`
   - Path: `./task-service`

2. Post a single PR comment via `gh pr comment "$(gh pr view --json url --jq '.url')" --body "<markdown>"` with a markdown table summarising all three SBOMs:

   ```markdown
   ## SBOM Summary

   | Module | Components | Flagged |
   |---|---|---|
   | adapters | <count> | <count or "none"> |
   | operator | <count> | <count or "none"> |
   | task-service | <count> | <count or "none"> |

   Generated via Snyk CycloneDX 1.4 — <ISO-8601 timestamp, e.g. 2026-04-18T10:00:00Z>
   
   *Note: Compute the current UTC timestamp and insert it literally before posting.*
   ```

   If `snyk_sbom_scan` returns no component count, write "N/A".
   If any module scan fails, write "FAILED: <error message>" in the Components column and "N/A" in the Flagged column for that module row.
