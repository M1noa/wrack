# Changelog

## Unreleased

## v0.2.0 (beta)

- renamed project to `wrack`
- task-grouped thread pools for concurrent delete / create / ban / settings
- churn mode: deletions run in parallel with raid creation
- smart-hammer rate-limit handling (wait the stated reset, abort on long bans)
- single-token speed tuning
- automated cross-platform release builds via GitHub Actions
