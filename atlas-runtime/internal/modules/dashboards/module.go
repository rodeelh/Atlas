package dashboards

// module.go — dashboards v2 platform module: lifecycle, wiring, and
// dependency injection. HTTP routes live in routes.go and skills.go
// registers the 12 granular agent-facing skills.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/skills"
)

// Module is the platform.Module implementation for v2 dashboards.
type Module struct {
	supportDir string
	dbPath     string

	store       *Store
	coordinator *Coordinator

	// Injected dependencies.
	runtime          RuntimeFetcher
	skillsRegistry   *skills.Registry
	skillExec        SkillExecutor
	db               *sql.DB
	liveRunner       LiveComputeRunner
	providerResolver func() (agent.ProviderConfig, error)
}

// New constructs a Module rooted at supportDir. dbPath is the sqlite path
// used by the read-only SQL resolver; the open *sql.DB handle is wired
// separately via SetDatabase for the chat_analytics and gremlin resolvers.
func New(supportDir, dbPath string) *Module {
	m := &Module{
		supportDir: supportDir,
		dbPath:     dbPath,
		store:      NewStore(supportDir),
	}
	m.coordinator = NewCoordinator(
		func(id string) (Dashboard, error) { return m.store.Get(id) },
		m.resolveSourceByName,
	)
	return m
}

// ID implements platform.Module.
func (m *Module) ID() string { return "dashboards" }

// Manifest implements platform.Module.
func (m *Module) Manifest() platform.Manifest {
	return platform.Manifest{Version: "v2"}
}

// Register implements platform.Module. Mounts protected routes onto the host.
func (m *Module) Register(host platform.Host) error {
	host.MountProtected(m.registerRoutes)
	return nil
}

// Start implements platform.Module. Archives any legacy v1 dashboards.json
// on first boot, then creates the AI live_compute runner if none was injected.
func (m *Module) Start(context.Context) error {
	if err := m.store.ArchiveV1IfPresent(); err != nil {
		return err
	}
	if m.liveRunner == nil {
		m.liveRunner = NewAILiveComputeRunner(m.providerConfigFromAgent)
	}
	return nil
}

// Stop implements platform.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Store returns the dashboard store (used by tests and skill handlers).
func (m *Module) Store() *Store { return m.store }

// Coordinator returns the SSE coordinator (used by tests and SSE route).
func (m *Module) Coordinator() *Coordinator { return m.coordinator }

// ── setters (called from cmd/atlas-runtime/main.go) ───────────────────────────

// SetRuntimeFetcher injects the loopback fetcher.
func (m *Module) SetRuntimeFetcher(f RuntimeFetcher) { m.runtime = f }

// SetSkillExecutor injects the skills registry as an executor. The adapter
// converts skills.ToolResult into the narrow skillExecResult used here.
func (m *Module) SetSkillExecutor(reg *skills.Registry) {
	m.skillsRegistry = reg
	if reg == nil {
		m.skillExec = nil
		return
	}
	m.skillExec = &skillRegistryAdapter{reg: reg}
}

// SetDatabase injects the shared *sql.DB handle.
func (m *Module) SetDatabase(db *sql.DB) { m.db = db }

// SetLiveComputeRunner injects the live_compute runner.
func (m *Module) SetLiveComputeRunner(r LiveComputeRunner) { m.liveRunner = r }

// SetProviderResolver injects the AI provider resolver.
func (m *Module) SetProviderResolver(fn func() (agent.ProviderConfig, error)) {
	m.providerResolver = fn
}

// ── resolver wiring ───────────────────────────────────────────────────────────

// resolverDeps snapshots the current wiring for use inside a resolver.
func (m *Module) resolverDeps() resolverDeps {
	// providerConfig snapshot is not strictly needed unless liveRunner looks
	// it up; we keep a light fallback via the injected provider resolver.
	return resolverDeps{
		runtime:          m.runtime,
		skills:           m.skillExec,
		db:               m.db,
		dbPath:           m.dbPath,
		liveRunner:       m.liveRunner,
		providerResolver: m.providerConfigFromAgent,
	}
}

func (m *Module) providerConfigFromAgent() (providerConfig, error) {
	if m.providerResolver == nil {
		return providerConfig{}, errors.New("provider resolver not wired")
	}
	raw, err := m.providerResolver()
	if err != nil {
		return providerConfig{}, err
	}
	return providerConfig{
		Type:         string(raw.Type),
		APIKey:       raw.APIKey,
		Model:        raw.Model,
		BaseURL:      raw.BaseURL,
		ExtraHeaders: raw.ExtraHeaders,
	}, nil
}

// resolveSourceByName fetches one source's fresh data.
func (m *Module) resolveSourceByName(ctx context.Context, dashboardID, sourceName string) (any, error) {
	d, err := m.store.Get(dashboardID)
	if err != nil {
		return nil, err
	}
	var src *DataSource
	for i := range d.Sources {
		if d.Sources[i].Name == sourceName {
			src = &d.Sources[i]
			break
		}
	}
	if src == nil {
		return nil, fmt.Errorf("%w: %s", ErrSourceMissing, sourceName)
	}

	// live_compute may depend on other sources — resolve them first.
	others := map[string]any{}
	if src.Kind == SourceKindLiveCompute {
		for _, other := range d.Sources {
			if other.Name == sourceName {
				continue
			}
			if val, err := resolveSource(ctx, m.resolverDeps(), other, nil); err == nil {
				others[other.Name] = val
			}
		}
	}
	return resolveSource(ctx, m.resolverDeps(), *src, others)
}

// ── skill executor adapter ────────────────────────────────────────────────────

type skillRegistryAdapter struct{ reg *skills.Registry }

func (a *skillRegistryAdapter) Execute(ctx context.Context, actionID string, args json.RawMessage) (skillExecResult, error) {
	res, err := a.reg.Execute(ctx, actionID, args)
	if err != nil {
		return skillExecResult{}, err
	}
	return skillExecResult{Success: res.Success, Summary: res.Summary, Artifacts: res.Artifacts}, nil
}

// ── widget resolver (for preview/resolve routes) ──────────────────────────────

// resolveWidgetData loads data for a single widget by resolving each of its
// bindings. Returns a map keyed by binding source name.
func (m *Module) resolveWidgetData(ctx context.Context, d Dashboard, w Widget) (any, error) {
	if len(w.Bindings) == 0 {
		return nil, nil
	}
	if len(w.Bindings) == 1 {
		// Single binding — return its payload directly so preset renderers
		// can consume it without a keyed envelope.
		return m.resolveProjectedBinding(ctx, d.ID, w.Bindings[0])
	}
	out := map[string]any{}
	for _, b := range w.Bindings {
		val, err := m.resolveProjectedBinding(ctx, d.ID, b)
		if err != nil {
			return nil, fmt.Errorf("binding %q: %w", b.Source, err)
		}
		out[b.Source] = val
	}
	return out, nil
}

func (m *Module) resolveProjectedBinding(ctx context.Context, dashboardID string, binding DataSourceBinding) (any, error) {
	val, err := m.resolveSourceByName(ctx, dashboardID, binding.Source)
	if err != nil {
		return nil, err
	}
	projected, ok := applyBindingProjection(val, binding)
	if !ok {
		return nil, fmt.Errorf("binding path %q was not found in source %q", binding.Path, binding.Source)
	}
	return projected, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func nowUTC() time.Time { return time.Now().UTC() }
