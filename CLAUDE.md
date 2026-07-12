# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Show Me The Story is a local, single-binary AI novel-writing web app. It calls a user-configured OpenAI-compatible API and persists each novel project as local JSON and Markdown files.

- **Backend:** Go 1.25+, a minimal `main` composition root plus layered `internal/` packages, standard library only.
- **Frontend:** Vite 5, Svelte 4, Tailwind CSS 4, and DaisyUI 5 in `frontend/`.
- **Delivery:** `frontend/dist/` is embedded into the Go executable through `embed.FS`; the application communicates with the browser using JSON REST and SSE.
- **Runtime:** the Go server listens on `:48090` by default (`PORT` overrides it). Vite uses `:5173` and proxies `/api` to the Go server.

This file is the single source of truth for coding-agent guidance; keep it focused on operational and architectural context needed to begin work.

## Commands

Run from the repository root unless stated otherwise.

```bash
# Full production build: install frontend dependencies, build frontend, build binary
task build

# Build just the embedded frontend assets
task frontend:build

# Build Go only (requires frontend/dist/ to already exist)
task build:go
go build -o show-me-the-story .

# Build then run the application
task dev

# Run Vite development server with HMR (backend must run separately on :48090)
task dev:frontend

# Backend test suite
go test ./...

# Targeted Go tests
go test -run '^TestNewV2RuntimeComposesUnselectedProjectDependencies$' .
go test -run '^TestResolveChatCompletionsURL$' ./internal/infra/openai

# Frontend build check
cd frontend && npm run build
```

The frontend has `dev`, `build`, and `preview` scripts only—there is no configured frontend test or lint script. `task frontend:build` uses `npm install`; use `cd frontend && npm ci` when a lockfile-reproducible dependency install is needed.

After `task clean`, or on a checkout without `frontend/dist/`, run `task frontend:build` or `task build` before a Go-only build: `web.go` embeds those production assets.

## Architecture

### Process, data, and HTTP boundary

- `main.go` selects the data directory (an existing first CLI-argument directory, otherwise the current directory), creates `storys/`, loads global API configuration, and starts the v2 HTTP composition.
- `server_v2.go` composes the application services, v2 REST/SSE server, project selection, global API configuration, and static frontend delivery.
- `internal/httpapi/` owns HTTP routing and request/response translation; `internal/app/` owns task-scoped story workflows; `internal/app/runtime/` owns the selected-project session and task lifecycle.
- `internal/infra/fsstore/` preserves the JSON/Markdown project format, including legacy data decoding, while `internal/infra/openai/` encapsulates provider communication.

The application distinguishes global API settings from project data. Do not change user story data incidentally:

```text
<data-dir>/
├── api.json                         # global API profiles / selected model
├── skills/                          # shared optional writing and polish skills
└── storys/<project>/
    ├── config.json                  # story settings, prompt overrides, enabled skills
    ├── progress.json                # outline, chapters, statuses, memories, foreshadows
    ├── settings.json                # characters, world, organizations, relations
    ├── postprocess.json             # full-book optimization state
    ├── sessions/                    # persisted assistant conversations
    └── Chapter_XX.md                # exported chapter prose
```

Use the atomic persistence helpers in `internal/infra/fsstore/` and `internal/infra/apiconfig/`. `api.json` is global; project `config.json` is not a substitute for it.

### Story workflow and backend ownership

The core workflow is: configure a project → generate/review/confirm an outline → generate and review chapters → summarize and fact-check each chapter → update foreshadows and narrative memory → persist project state and chapter Markdown. Full-book diagnosis and targeted revisions reuse the same persisted state.

The v2 backend is layered by responsibility:

- `internal/domain/project/`: persisted story domain models and prompt/skill defaults.
- `internal/app/outline`, `writing`, `foreshadow`, `postprocess`, `settings`, `continuation`, `agent`, and `chat`: workflow services.
- `internal/httpapi/`: REST endpoints and response contracts; `internal/infra/sse/`: task/event streaming.
- `internal/infra/fsstore/`: atomic filesystem persistence and JSON/Markdown compatibility.
- `internal/infra/apiconfig/`, `openai/`, and `skills/` own global provider configuration, provider transport, and optional writing-rule discovery respectively. Root Go files are limited to application composition and static delivery.

Prompt placeholders are literal `{{.KeyName}}` values replaced by `RenderPrompt`; they are not Go templates. Keep non-empty persisted prompt overrides intact.

### Async AI work and SSE

AI operations are mutually exclusive and normally asynchronous:

1. An HTTP handler starts work through `internal/app/runtime.TaskManager` and returns `202 Accepted`.
2. Work runs in a goroutine and must always release its task lease.
3. The task context lets `/api/task/stop` cancel provider calls and retry waits.
4. Task state and streamed prose reach the client through `internal/infra/sse/`.
5. Synchronous mutating endpoints are rejected while a task is active.

When adding an async endpoint: register it in `internal/httpapi/`, release the task lease on every exit path, and broadcast persisted state updates.

### Frontend and localization

- `frontend/src/App.svelte` composes the app shell and workflow pages.
- `frontend/src/pages/` holds page workflows.
- `frontend/src/lib/api.js` centralizes API calls, locale headers, and error normalization.
- `frontend/src/lib/sse.js` owns EventSource reconnecting and streamed-content buffering; do not store complete streamed chapters in Svelte stores while generation is active.
- `frontend/src/lib/stores.js` contains cross-page state; keep page-local presentation state out of it.
- `frontend/src/lib/i18n/zh.js` and `en.js` must remain in sync for frontend-facing strings.

Project language controls AI prompts, generated prose, skill filtering, and agent prompts. UI language is independent. For user-visible behavior, keep frontend Chinese and English strings synchronized and update language-specific defaults in `internal/domain/project/prompts_zh.go` and `prompts_en.go`.

### Assistant safety model

The in-app assistant can edit local project data. Preserve its existing safeguards:

- Editing is distinct from deletion; use revision or targeted-edit behavior for content changes.
- `delete_chapter`, `delete_chapters_from`, `delete_outline`, and `reset_progress` need explicit confirmation.
- Whole-outline regeneration is blocked after accepted chapters; continuation appends pending chapters instead.
- Do not silently overwrite populated story configuration—route AI proposals through pending config changes.
- Use `agentMsg`/`agentErr` for assistant results so persisted chat can render in either UI language.

## Repository constraints

- Keep the Go backend standard-library-only unless the user explicitly approves a dependency/design change.
- Skills are opt-in: only inject them into functional AI paths when enabled in project configuration.
- Keep generated `frontend/dist/` output out of ordinary source edits.
- No Cursor rules, `.cursorrules`, or Copilot instructions are present in this repository.
