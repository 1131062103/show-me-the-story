# AGENTS.md - AI coding agent guide

This file is the always-on guide for coding agents working in this repository. Keep it concise and actionable. Link to existing docs instead of copying long reference material.

## Project Snapshot

Show Me The Story is a single-binary Go web app for AI-assisted long-form novel generation.

- Backend: Go 1.25+, module `showmethestory`, single package in the repository root, standard library only.
- Frontend: Vite 5 + Svelte 4 + Tailwind CSS 4 + DaisyUI 5 under [frontend/](frontend/); production assets build to [frontend/dist/](frontend/dist/) and are embedded by Go.
- Runtime data: `api.json` is global; story projects live under `storys/<project>/` with `config.json`, `progress.json`, `settings.json`, `sessions/`, and `Chapter_XX.md`.
- Default backend port: `:48090` (`PORT` overrides). Vite dev server runs on `:5173` and proxies `/api` to the backend.
- User-facing docs: [README.md](README.md) and [README.en.md](README.en.md). Do not duplicate those docs here.

## Build, Test, Run

Use the repo root as the working directory.

```bash
task build             # full build: npm install + npm run build + go build
task build:go          # Go-only build; requires frontend/dist to already exist
task dev               # full build, then run the backend binary
task dev:frontend      # Vite dev server with /api proxy to :48090
go test ./...          # run Go tests
go build -o show-me-the-story .
```

Before finishing code changes, run the narrowest relevant test first. For broad backend changes, run `go test ./...` and `go build -o show-me-the-story .`. For frontend changes, run `cd frontend && npm run build`.

## Architecture Map

Backend files are intentionally flat in the root package. Prefer adding behavior near the owning file instead of creating new packages.

- [main.go](main.go): process directory, `storys/` setup, global API config load, server startup.
- [web.go](web.go): route registration, middleware, static file serving.
- [handlers.go](handlers.go): HTTP handlers, project switching, task mutex, auto-confirm loop, async task entrypoints.
- [api.go](api.go): OpenAI-compatible API URL resolution, model fetching, sync/stream calls, retry, token accounting hooks.
- [config.go](config.go), [state.go](state.go), [settings.go](settings.go): persisted project config, progress, and structured settings.
- [outline.go](outline.go), [outline_helpers.go](outline_helpers.go), [outline_character.go](outline_character.go): outline generation, validation, locks, and character checks.
- [writing.go](writing.go), [writing_length.go](writing_length.go), [writing_delete.go](writing_delete.go), [writing_conflict.go](writing_conflict.go): chapter generation, revision, length control, delete/reject semantics, and conflict handling.
- [foreshadow.go](foreshadow.go), [foreshadow_consistency.go](foreshadow_consistency.go): foreshadow lifecycle and outline consistency checks.
- [postprocess.go](postprocess.go): full-book diagnosis, consistency checks, roadmap, and execution.
- [agent.go](agent.go), [agent_i18n.go](agent_i18n.go), [chat.go](chat.go): assistant Agent Loop, tools, localized tool messages, chat persistence.
- [prompts.go](prompts.go), [prompts_en.go](prompts_en.go), [locale.go](locale.go), [messages.go](messages.go), [i18n_inject.go](i18n_inject.go): prompt defaults and i18n.
- [skills.go](skills.go), [embeds/skills/](embeds/skills/): built-in and project skills.
- [frontend/src/pages/](frontend/src/pages/): main Svelte screens; [frontend/src/lib/](frontend/src/lib/) contains API, SSE, stores, i18n, and shared utilities.

## Core Conventions

- Go backend must stay standard-library-only. Do not add third-party Go modules unless the user explicitly approves a design change.
- Persisted JSON writes should use existing atomic helpers (`writeFileAtomic` or local save functions that wrap it).
- Prompt rendering uses simple `strings.ReplaceAll` via `RenderPrompt`; placeholders are literal `{{.KeyName}}`, not Go templates.
- API config and story config are separate: global `api.json` vs per-project `config.json`.
- Skills are opt-in. Do not inject skills into functional AI paths unless the project config has enabled them.
- User-filled story config fields are protected. AI-suggested changes to non-empty fields should go through pending config changes, not silent overwrite.
- Runtime project data and user stories under `storys/` are local artifacts. Avoid modifying them unless the user specifically asks to change project data.
- Keep generated front-end build output out of normal edits unless the task is a release/build artifact task.

## Async Task Pattern

AI operations are mutually exclusive and usually asynchronous:

1. Handler calls `tryStartTask()` and returns `202 Accepted`.
2. Work runs in a goroutine with `defer h.endTask()`.
3. Task code uses `h.taskCtx` so `/api/task/stop` can cancel API calls and retry sleeps.
4. Progress, logs, chunks, and token usage are pushed through SSE in [logger.go](logger.go) and [tokens.go](tokens.go).
5. Synchronous edit endpoints should call `rejectIfTaskRunning(w)` to avoid data races while AI work is active.

When adding an async endpoint, register the route in [web.go](web.go), add localized task/log messages, broadcast progress after state changes, and make sure errors release the task lock.

## Frontend Patterns

- UI text must use i18n keys from [frontend/src/lib/i18n/zh.js](frontend/src/lib/i18n/zh.js) and [frontend/src/lib/i18n/en.js](frontend/src/lib/i18n/en.js).
- API requests go through [frontend/src/lib/api.js](frontend/src/lib/api.js), which sends locale headers and normalizes errors.
- SSE handling lives in [frontend/src/lib/sse.js](frontend/src/lib/sse.js). Long streaming text is intentionally buffered and windowed; do not store full streamed chapters in Svelte stores during generation.
- Global state belongs in [frontend/src/lib/stores.js](frontend/src/lib/stores.js). Keep page components focused on workflow and presentation.
- Use the existing DaisyUI/Tailwind visual language unless the task explicitly asks for a redesign.

## I18n and Prompt Changes

Any user-visible backend message, frontend label, prompt, system prompt, or injected prompt block must stay bilingual.

- Backend logs/tool status: add or update keys in [messages.go](messages.go), then mirror them in `zh.js` and `en.js` when the frontend renders them.
- API errors: use `writeErrorReq` and keys from [locale.go](locale.go).
- Default prompts: update both [prompts.go](prompts.go) and [prompts_en.go](prompts_en.go), and ensure [config.go](config.go) applies defaults for new fields.
- Prompt injection text: route language-specific content through [i18n_inject.go](i18n_inject.go).
- System prompts: use `SystemPromptFor` entries in [locale.go](locale.go).

## Agent and Destructive Operations

The in-app assistant in [agent.go](agent.go) can mutate project data. Preserve its safety model:

- Modification is not deletion. Use revision/edit tools for content changes.
- `delete_chapter`, `delete_chapters_from`, `delete_outline`, and `reset_progress` require explicit confirmation parameters.
- Regenerating a whole outline is blocked once accepted chapters exist; continuation should append pending outline chapters instead.
- Changing chapter count for a fresh project should update project config and regenerate outline, not delete chapters as a shortcut.
- Tool results should use `agentMsg`/`agentErr` so persisted chat messages can be rendered in either UI language.

## Validation Checklist

Use the smallest relevant subset, then broaden if the change crosses boundaries.

- Backend logic: `go test ./...`; for build-sensitive work also `go build -o show-me-the-story .`.
- URL behavior: `go test ./... -run TestResolveChatCompletionsURL`.
- Prose counting or length control: run the matching `prose_units` or `writing_length` tests.
- Delete/reject/paragraph lock behavior: run the matching `writing_*_test.go` tests.
- Frontend/i18n/SSE changes: `cd frontend && npm run build`.
- Full release-style verification: `task build`.

## Keep This File Current

Update this file when a project convention, build command, architecture boundary, safety rule, or i18n requirement changes. Do not expand it into a full API reference; link to [README.md](README.md), [README.en.md](README.en.md), or the owning source files instead.
