# kape-io Project Instructions

## PR Checklist

Before creating any PR, run SBOM generation for all Go modules and post a summary comment:

1. Run `snyk_sbom_scan` MCP tool on each Go module:
   - Path: `./adapters`
   - Path: `./operator`
   - Path: `./task-service`

2. Post a single PR comment via `gh pr comment <PR-URL> --body "<markdown>"` with a markdown table summarising all three SBOMs:

   ```markdown
   ## SBOM Summary

   | Module | Components | Flagged |
   |---|---|---|
   | adapters | <count> | <count or "none"> |
   | operator | <count> | <count or "none"> |
   | task-service | <count> | <count or "none"> |

   Generated via Snyk CycloneDX 1.4 — $(date -u +"%Y-%m-%dT%H:%M:%SZ")
   ```

   If `snyk_sbom_scan` returns no component count, write "N/A".
   If any module scan fails, note the failure in the table row instead of blocking the PR.
