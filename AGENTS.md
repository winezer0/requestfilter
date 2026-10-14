@AGENTS.common.md

# Project Specifications

1. When reading Chinese documents, pay attention to the file encoding, prioritize processing as UTF-8, and promptly verify the encoding if garbled text appears.

## Tanscan Project Rules

1. This repository implements the Tanscan Web vulnerability scanning platform described in the planning and design documents. Apply the shared engineering rules in `AGENTS.common.md` when they fit this project's Go backend and React frontend.
2. Rules specific to mgcodesec parsers, source-position/index contracts, code graphs, parser documentation, `vulndemo` fixtures, and mgcodesec workspace scripts or cache paths do not apply here.
3. Database bootstrap for a fresh Tanscan installation is required. During the current development phase, do not implement upgrades or data migrations from earlier released schemas; revisit versioned migration support before promising schema compatibility.
4. Add tests around core behavior and security boundaries, including target validation, scope filtering, SSRF defenses, task state transitions, and scanner result normalization. Choose unit and integration coverage based on risk; the mgcodesec-specific fixture rules and fixed 80% package coverage target do not apply.
5. Keep project build, test, and cache commands documented in Tanscan's own repository configuration and scripts; do not assume mgcodesec workspace commands or paths.
6. API keys are limited to scan task creation, ready-task creation/start, task list/status/detail/result reads, and task cancellation. Browser login, password changes, and API-key management require the administrator's browser session.
