# SGo-Scraper — Version history

There is one file per version, newest first. Each file records what was fixed or changed and why.

| Version | Date | Summary |
|---------|------|---------|
| [v1.4.1](v1.4.1.md) | 2026-10-31 | Pause between listing and comment pages to ease the candids-listing 500 errors; `[retry ok]` line when a retry succeeds |
| [v1.4.0](v1.4.0.md) | 2026-10-31 | Finished items skipped without loading their pages, end-of-run summary, `SHOW_SKIPS=off` to hide skip lines |
| [v1.3.1](v1.3.1.md) | 2026-10-31 | Shorter error messages: the URL is no longer repeated in retry and failure lines |
| [v1.3.0](v1.3.0.md) | 2026-10-31 | Colored console output for headings, progress, success, retries, errors and skips; `COLOR=off` / `NO_COLOR` to disable |
| [v1.2.0](v1.2.0.md) | 2026-10-31 | Retries for network errors, listing failures no longer end a run, login check, URL validation, video stall timeout, correct image extensions, shared HTTP client, watcher `.txt` support and quit-on-error fix, unit tests, git repo |
| [v1.1.0](v1.1.0.md) | 2026-10-31 | Reliability fixes: `rscivid` cookie sent, exact ID matching, items recorded only when complete, `.part` downloads, stable group-thread order, failures no longer stop sweeps, watcher `Failed` folder |
| [v1.0.0](v1.0.0.md) | before 2026-10-31 | Baseline as received, with the issues found in review |

## Adding a new version

1. Copy the newest version file to `vX.Y.Z.md`. Use a patch bump for fixes, a minor bump for new features or behaviour changes, and a major bump for breaking `.env` or database changes.
2. Fill in the date, the previous version, and the **Fixed** / **Changed** / **Not changed** sections.
3. Add a row to the top of the table above.
