# Dashboards v3 Implementation Plan

This document is the working plan for improving the Atlas dashboards feature while keeping the existing dashboard viewer, refresh flow, presets, and code-widget sandbox stable.

The implementation should follow these phases in order. Each phase should preserve backward compatibility unless the plan explicitly calls out a migration.

## Current Architecture Summary

- The web dashboard experience is primarily a viewer. Dashboard creation and mutation are driven through runtime skills.
- The frontend renders dashboard lists, detail views, source health, refresh state, preset widgets, and sandboxed code widgets.
- Widgets bind to dashboard sources by source name. The frontend currently relies mostly on widget options for paths and rendering configuration.
- The backend owns dashboard authoring skills, source resolution, commit validation, layout packing, code-widget compilation, and SSE refresh events.
- Code widgets render in a sandboxed iframe with network blocked and data supplied by the parent window.

## Phase 0: Baseline Safety

Goal: protect the dashboard behavior that already works before changing product behavior.

### Scope

- Add regression coverage for current dashboard list and detail loading.
- Test source hydration through mocked or fixture-backed SSE events.
- Test the refresh button loading/recovery behavior.
- Test existing preset rendering for metric, table, line chart, bar chart, list, and markdown widgets.
- Test code widgets still sandbox correctly and receive parent-supplied data.
- Add backend tests around dashboard authoring skills, commit validation, refresh, event replay, and cached source behavior.
- Add representative dashboard JSON fixtures covering all current widget modes.

### Guardrails

- No product behavior changes in this phase.
- Fixtures must represent real current dashboard contracts.
- Tests should make existing behavior easier to refactor, not freeze accidental implementation details unnecessarily.

### Exit Criteria

- Current frontend dashboard tests pass.
- Current backend dashboard tests pass.
- New fixtures cover the main dashboard shapes we need to preserve.
- We have at least one end-to-end hydration test that exercises dashboard detail, source update, and widget rendering.

## Phase 1: Widget Customizer / Editor

Goal: make dashboard widget customization available in the UI while preserving the current agent-driven authoring workflow.

### Scope

- Add draft/live awareness to the dashboard detail screen.
- Keep live dashboards viewer-first and safe by default, but expose an edit entry point for live dashboards.
- Editing a live dashboard should create or resume an editable draft revision from the current live dashboard, then publish/commit back to live only after explicit user confirmation.
- Add a draft widget inspector panel for:
  - Title
  - Description
  - Preset type
  - Binding source
  - Widget size and grid placement
  - Format options
  - Chart axis options
  - Table column options
  - List limits
  - Color and style controls
- Add backend draft mutation endpoints or a thin HTTP wrapper over the existing dashboard skills.
- Reuse existing validation and packing logic rather than forking authoring behavior.
- Return the full updated dashboard definition after each successful mutation.
- Start with explicit saves. Add autosave only after the mutation path is stable.
- Ship visual drag, reorder, and resize as a later sub-step after form-based layout editing works.
- Ship visual drag, reorder, and resize with a proven dashboard grid library instead of custom pointer/collision math when the library fits the Atlas stack.

### Guardrails

- Direct mutation remains enabled only for draft dashboards.
- Live dashboard editing must go through an explicit edit-draft flow so working dashboards do not change until the user publishes the draft.
- Existing live dashboard rendering must remain untouched.
- A failed save must leave the previous widget config intact.
- All mutations should use the same validation rules as skills.
- Drag and resize interactions must snap to the existing 12-column dashboard grid and serialize back to `gridX`, `gridY`, `gridW`, and `gridH`.
- Inline editing should cover common widget metadata such as title and description while preserving the inspector for deeper configuration.

### Exit Criteria

- A user can open a draft dashboard, edit a widget option, save it, and see the updated render.
- Existing live dashboards render as before.
- Tests cover successful draft edit, failed validation, and unchanged live-dashboard viewing.

## Phase 2: Schema-Aware Bindings

Goal: make widget data paths accurate, explicit, and reusable.

### Scope

- Define binding projection semantics:
  - `binding.source` selects the source.
  - `binding.path` projects into the source payload.
  - `binding.options` provides binding-level transforms and defaults.
  - Widget `options` remain presentation-specific.
- Implement projection in one shared backend path for widget data resolution.
- Keep source-level SSE payloads compatible initially.
- Preserve current behavior when `binding.path` is omitted.
- Improve supported path syntax:
  - Current dot paths continue to work.
  - Add array index support such as `items[0].name`.
  - Add array projection conventions such as `rows[].value`.
- Align shape suggestions with frontend option names:
  - `labelKey`
  - `subKey`
  - `x`
  - `y`
  - `seriesPath`
  - `columns`
- Return machine-readable suggested widget options from shape inference.
- Validate widget options against projected data during commit.

### Guardrails

- Existing dashboards without binding paths must keep receiving the same effective data.
- Current widget options such as `path`, `seriesPath`, and `itemsPath` must continue to work.
- Prefer additive behavior and deprecation warnings before changing authoring defaults.
- Transient source outages should produce warnings when appropriate, not destructive dashboard changes.

### Exit Criteria

- Backend tests cover binding projection.
- Commit validation uses projected data where a binding path is present.
- Existing dashboards pass unchanged.
- New dashboards can use binding paths without duplicating data paths in widget options.

## Phase 3: Widget Interactions

Goal: make widgets useful beyond static display.

### Scope

- Add local preset-widget interactions first:
  - Table sorting
  - Table search/filtering
  - Table pagination
  - List expand/collapse
  - Chart tooltip improvements
  - Metric drilldown
- Add a widget/source drilldown drawer showing raw data, source metadata, and recent health.
- Add dashboard-level interaction state for:
  - Time range
  - Search/filter context
  - Selected source
  - Selected widget
- Add source refresh actions:
  - Refresh all sources
  - Refresh one source
  - Refresh the sources bound to one widget
- Extend the code-widget parent message protocol with safe actions:
  - `atlas:refresh-source`
  - `atlas:open-drilldown`
  - `atlas:set-filter`
  - `atlas:navigate-dashboard`
- Add safe `@atlas/ui` interaction primitives for code widgets:
  - Button
  - Tabs
  - Select
  - Details
  - TimeRangePicker

### Guardrails

- Interactions should be progressive enhancement over the current render.
- No widget interaction should mutate source data unless routed through an approved parent action.
- Code widgets must keep direct network access blocked.
- Parent message handling must validate requested actions and source names.

### Exit Criteria

- Tables can sort, filter, and paginate without breaking existing table config.
- Users can refresh a single source or a widget's bound sources.
- Code widgets can request approved interactions through the parent protocol.
- Tests confirm blocked network behavior remains intact.

## Phase 4: Freshness, Provenance, Accuracy

Goal: help users understand whether dashboard data is fresh, stale, failed, or partially validated.

### Scope

- Expand refresh events with optional metadata:
  - `success`
  - `durationMs`
  - `sourceKind`
  - `resolvedAt`
  - `lastSuccessfulAt`
  - `stale`
  - `cacheAgeMs`
  - `validationWarnings`
- Preserve backward compatibility by keeping current event fields and making new fields optional.
- Add a richer source health model:
  - Loading
  - Fresh
  - Stale
  - Failed with last good data
  - Timed out
  - Schema warning
- Add widget provenance UI:
  - Source name
  - Source kind
  - Last updated
  - Refresh duration
  - Last successful refresh
  - Validation state
  - Raw sample or drilldown view
- Fix refresh policy behavior:
  - Implement `IdleSeconds` or remove it from the public contract.
  - Add retry/backoff for flaky sources.
  - Preserve stale-while-revalidate behavior so widgets do not blank out unnecessarily.

### Guardrails

- Never replace last good data with an error payload.
- Failed refreshes should update health, not destroy widget content.
- Frontend contracts must tolerate old events without new metadata.
- Provenance UI should be informative without making the main dashboard noisy.

### Exit Criteria

- Backend events include richer metadata.
- Frontend displays fresh, stale, failed, timed out, and recovered states correctly.
- Last good data remains visible after a failed refresh.
- Tests cover failure, timeout, recovery, and stale data.

## Phase 5: More Presets + Code Widget Authoring

Goal: make dashboards more expressive without requiring custom code for common layouts.

### Scope

- Add new presets gradually:
  - Progress
  - Gauge
  - Status grid
  - KPI group
  - Timeline
  - Heatmap/calendar
  - Area chart
  - Stacked chart
  - Pie/donut chart
  - Scatter chart
- Add shared style options:
  - Thresholds
  - Conditional color
  - Unit labels
  - Empty-state text
  - Error-state text
  - Value mapping
  - Palette selection
- Add `dashboard.add_code_widget`.
- Compile code widgets at add/update time and return diagnostics before commit.
- Expand safe `@atlas/ui` components while keeping the sandbox deterministic and data-only.
- Update dashboard docs and authoring guidance:
  - Remove stale `custom_html` references if unsupported.
  - Remove stale `web` source references if unsupported.
  - Document supported source kinds.
  - Document supported presets.
  - Document refresh event fields.
  - Document code-widget limits.

### Guardrails

- New presets must not alter existing preset behavior.
- Code-widget authoring must stay behind compile validation.
- Existing dashboards must keep rendering exactly as before.
- New style options should have conservative defaults.

### Exit Criteria

- Snapshot or render tests cover every preset.
- Compile tests cover valid and invalid code widgets.
- At least one mixed dashboard uses existing presets, new presets, and a code widget.
- Docs match the actual TypeScript and Go contracts.

## Recommended Ship Order

1. Phase 0: tests and fixtures.
2. Phase 1A: draft widget inspector without drag/drop.
3. Phase 2A: binding projection with backward compatibility.
4. Phase 3A: table, list, and chart interactions.
5. Phase 4A: richer source health and provenance metadata.
6. Phase 5A: two or three high-value presets first.
7. Phase 1B: visual layout editing.
8. Phase 3B: code-widget interaction protocol.
9. Phase 5B: broader preset library and code-widget authoring.

## Ongoing Rules

- Keep the existing dashboard viewer working at every phase.
- Prefer additive contract changes.
- Make old dashboards render without migration.
- Put new authoring behavior behind draft-only flows until stable.
- Reuse runtime skill validation instead of creating parallel validation logic.
- Verify each phase with frontend tests, backend tests, and at least one manual smoke pass.
- Update this document if implementation discoveries require changing the plan.

## Implementation Log

### Phase 0: Completed

- Added reusable dashboard browser fixtures covering all current preset modes plus code-widget fixture coverage.
- Expanded Playwright hydration coverage for source success, source failure, refresh, and stale-while-refresh behavior.
- Added backend coordinator tests for initial seed, cached replay, error propagation, and unsubscribe cleanup.

### Phase 1A: Completed

- Added a draft-only HTTP widget update route.
- Added a shared backend draft widget update helper with binding validation, preset validation, and layout repacking.
- Added TypeScript contracts and client support for widget updates.
- Added a draft-only widget inspector in the dashboard detail screen.
- Added browser coverage for explicit draft widget edits.
- Drag/drop layout editing remains deferred to Phase 1B as planned.

### Phase 1B: Completed

- Added GridStack as the visual dashboard layout engine instead of custom drag/resize collision code.
- Added a draft layout update API that saves widget `gridX`, `gridY`, `gridW`, and `gridH` in one validated batch.
- Added backend layout validation for bounds and widget overlap.
- Added live-dashboard edit flow that creates or resumes a draft revision from the live dashboard.
- Added draft publish support that commits a live-based draft back over the original live dashboard.
- Added snap-grid drag and resize editing in the dashboard UI with move handles, resize handles, and autosaved layout changes.
- Added inline title and description editing for selected draft widgets.
- Added browser coverage for opening a live dashboard into an editable snap-grid draft.

### Phase 2A: Completed

- Added compatible binding projection for backend widget resolution.
- Added commit validation against projected binding data when `binding.path` is present.
- Expanded binding path support to include dot paths, array indexes such as `items[0]`, numeric segments such as `items.0`, and array projections such as `rows[].value`.
- Added frontend projection for source-backed widgets so SSE rendering honors `binding.path` without changing source-level event payloads.
- Added binding path editing to the draft widget inspector.
- Updated shape hints to use current frontend option names and added machine-readable `suggestedOptions`.

### Phase 3A: Completed

- Added local table search.
- Added sortable table columns.
- Added table pagination with configurable `pageSize`.
- Added list expand/collapse with configurable `visibleCount`.
- Added chart point counts and click-to-select chart point details.
- Added browser coverage for table search/pagination and list expansion.

### Phase 3B: Completed

- Added a safe code-widget action bridge over the existing iframe `postMessage` channel.
- Added dashboard action handling for `refresh-source`, `open-drilldown`, `set-filter`, and `navigate-dashboard`.
- Added a one-source refresh API and coordinator path so code widgets can refresh a specific source without forcing a full dashboard refresh.
- Added drilldown UI for inspecting raw widget/source payloads.
- Added lightweight dashboard interaction context with source-filter chips and generic filter state.
- Expanded `@atlas/ui` with action-capable `Button`, `Tabs`, `Select`, `Details`, `TimeRangePicker`, and an `actions` helper for code widgets.
- Added backend and browser coverage for code-widget interaction requests.

### Phase 4A: Completed

- Added optional refresh event metadata: `success`, `sourceKind`, `resolvedAt`, `durationMs`, `lastSuccessfulAt`, `stale`, and `cacheAgeMs`.
- Preserved last-good source data when a later refresh fails.
- Added stale source health state in the frontend.
- Added per-widget stale warning UI while continuing to render last-good data.
- Added provenance details to source health badges.
- Added backend tests for metadata and stale event behavior.
- Added browser coverage for stale-while-error rendering.

### Phase 5A: Completed

- Added the first high-value preset expansion: `progress`, `gauge`, and `status_grid`.
- Added backend preset validation and commit-readiness coverage for the new presets.
- Added frontend renderers, styling, and accessible progress semantics for the new presets.
- Extended dashboard authoring skill schemas and the draft widget inspector preset list.
- Expanded dashboard hydration fixtures and browser coverage to render all three new presets.

### Phase 5B: Completed

- Added a second preset expansion pass with `area_chart`, `pie_chart`, and `kpi_group`.
- Added backend preset validation and commit coverage for the new presets.
- Added frontend renderers and styling for trend, composition, and KPI-cluster layouts.
- Expanded the hydration fixture and browser coverage to render and refresh the new preset set.
- Added draft code-widget TSX editing in the widget inspector with compile-on-save behavior.
- Surfaced compile failures directly in the draft inspector so invalid TSX is actionable.
- Added a built-in code widget starter template and compile metadata in the inspector.
- Added backend and browser coverage for valid and invalid draft code-widget edits.
- Improved code-widget compile diagnostics with line and column context from esbuild.
- Added code-widget authoring status, reusable example snippets, and clearer saved-versus-unsaved feedback in the draft inspector.
- Added the remaining preset family: `donut_chart`, `scatter_chart`, `stacked_chart`, `timeline`, and `heatmap`.
- Added shared style controls across preset widgets for thresholds, unit labels, empty-state text, error-state text, value mapping, and palette selection.
- Added the `dashboard.add_code_widget` skill so code widgets compile at add time instead of waiting for commit-only validation.
- Expanded backend validation and browser fixtures so every supported preset now renders in the hydration suite.

### Layout Editing Cleanup

- Reduced layout editor save thrash by keeping a stable GridStack instance during edit sessions instead of reinitializing it after each saved move.
- Changed layout saves to trigger from drag-stop and resize-stop boundaries so dragging feels less jumpy and more predictable.
- Added click-to-select behavior in edit mode so choosing a widget for the inspector feels more direct.
- Added clearer visible resize affordances and hover polish for layout-editing states.
