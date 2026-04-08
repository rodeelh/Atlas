// Command Atlas is the Atlas Go runtime — the full replacement for AtlasRuntimeService.
// It serves the complete Atlas HTTP API natively in Go. No Swift backend is required.
//
//	Atlas [-port 1984] [-web-dir ./web]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/auth"
	"atlas-runtime-go/internal/browser"
	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/comms"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/domain"
	"atlas-runtime-go/internal/engine"
	"atlas-runtime-go/internal/forge"
	"atlas-runtime-go/internal/location"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/mind"
	apivalidationmodule "atlas-runtime-go/internal/modules/apivalidation"
	approvalsmodule "atlas-runtime-go/internal/modules/approvals"
	automationsmodule "atlas-runtime-go/internal/modules/automations"
	communicationsmodule "atlas-runtime-go/internal/modules/communications"
	dashboardsmodule "atlas-runtime-go/internal/modules/dashboards"
	enginemodule "atlas-runtime-go/internal/modules/engine"
	forgemodule "atlas-runtime-go/internal/modules/forge"
	skillsmodule "atlas-runtime-go/internal/modules/skills"
	usagemodule "atlas-runtime-go/internal/modules/usage"
	voicemodule "atlas-runtime-go/internal/modules/voice"
	workflowsmodule "atlas-runtime-go/internal/modules/workflows"
	"atlas-runtime-go/internal/platform"
	"atlas-runtime-go/internal/preferences"
	"atlas-runtime-go/internal/runtime"
	"atlas-runtime-go/internal/server"
	"atlas-runtime-go/internal/skills"
	"atlas-runtime-go/internal/storage"
	"atlas-runtime-go/internal/voice"
)

func main() {
	portFlag := flag.Int("port", 0, "Override the HTTP port (default: value from config.json)")
	webDirFlag := flag.String("web-dir", "", "Path to the built atlas-web/dist directory")
	flag.Parse()

	// ── Config ────────────────────────────────────────────────────────────────
	cfgStore := config.NewStore()
	cfg := cfgStore.Load()

	port := cfg.RuntimePort
	if *portFlag > 0 {
		port = *portFlag
	}

	// ── First-run seeding ─────────────────────────────────────────────────────
	// Seed MIND.md and SKILLS.md if they don't exist yet. No-ops on subsequent runs.
	if err := mind.InitMindIfNeeded(config.SupportDir()); err != nil {
		log.Printf("Atlas: warn: seed MIND.md: %v", err)
	}
	if err := mind.InitSkillsIfNeeded(config.SupportDir()); err != nil {
		log.Printf("Atlas: warn: seed SKILLS.md: %v", err)
	}

	// ── Location + Preferences ────────────────────────────────────────────────
	// Load persisted location and locale preferences into memory immediately,
	// then refresh location from the public IP in the background if stale.
	preferences.LoadFromConfig()
	location.LoadFromConfig()
	if location.ShouldRefresh() {
		go func() {
			if err := location.DetectFromIP(); err != nil {
				log.Printf("Atlas: location detection: %v", err)
			}
		}()
	}

	// ── Database ──────────────────────────────────────────────────────────────
	dbPath := config.DBPath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		log.Fatalf("Atlas: create support dir: %v", err)
	}
	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("Atlas: open database: %v", err)
	}
	defer db.Close()

	// Backfill any existing token_usage rows that have $0 cost due to a missing
	// pricing entry (e.g. preview/experimental model variants added after the row
	// was written). Runs synchronously at startup — fast because it only touches
	// the zero-cost subset.
	if n := db.BackfillTokenUsageCosts(); n > 0 {
		log.Printf("Atlas: backfilled costs for %d token usage record(s)", n)
	}

	// ── Services ──────────────────────────────────────────────────────────────
	authSvc := auth.NewService(db)
	runtimeSvc := runtime.NewService(port)
	bc := chat.NewBroadcaster()

	// Browser manager — launched lazily on first browser.* skill call.
	// Runs headless by default; set browserShowWindow: true in go-runtime-config.json
	// to open a visible Chrome window (useful for debugging or demos).
	goCfg := config.LoadGoConfig()
	browserMgr := browser.New(db, !goCfg.BrowserShowWindow)
	defer browserMgr.Close()

	// Voice manager — whisper-server (Phase 1) + piper (Phase 2) subprocess lifecycle.
	// Both servers start when a voice session begins and stop together on session
	// end or idle timeout, so no voice-related RAM is resident when voice is off.
	voiceMgr := voice.NewManager(config.AtlasInstallDir(), config.VoiceModelsDir())
	voiceIdle := cfg.VoiceSessionIdleSec
	if voiceIdle <= 0 {
		voiceIdle = 300
	}
	voiceMgr.SetIdleTimeout(time.Duration(voiceIdle) * time.Second)
	defer voiceMgr.Close()

	skillsRegistry := skills.NewRegistry(config.SupportDir(), db, browserMgr)
	skillsRegistry.SetVoiceManager(voiceMgr)

	// Load user-installed custom skills from ~/Library/Application Support/ProjectAtlas/skills/.
	// Non-fatal: a broken custom skill never prevents Atlas from starting.
	skillsRegistry.LoadCustomSkills(config.SupportDir())

	// Wire vision inference into the skills registry so browser.solve_captcha
	// can call the active AI provider directly from within a skill function.
	// The closure re-resolves the provider on every call so runtime config
	// changes (e.g. switching from OpenAI to Anthropic) are picked up immediately.
	skillsRegistry.SetVisionFn(func(ctx context.Context, imageB64, prompt string) (string, error) {
		prov, err := chat.ResolveProvider(cfgStore.Load())
		if err != nil {
			return "", fmt.Errorf("vision: no AI provider configured: %w", err)
		}
		return agent.CallVision(ctx, prov, imageB64, prompt)
	})

	// Engine LM — bundled llama-server subprocess manager.
	engineMgr := engine.NewManager(config.AtlasInstallDir(), config.ModelsDir())

	// Phase 3 Tool Router — second llama-server instance on AtlasEngineRouterPort (default 11986).
	// Shares the same binary and models dir as the primary engine; just a different port + model.
	routerMgr := engine.NewManager(config.AtlasInstallDir(), config.ModelsDir())

	engineMgr.SetIdleTimeout(60 * time.Minute) // eject primary model after 60 min idle
	routerMgr.SetIdleTimeout(12 * time.Hour)   // eject router model after 12 hr idle
	engineMgr.SetMlock(cfg.AtlasEngineMlock)   // pin model in RAM (configurable via UI)
	routerMgr.SetMlock(cfg.AtlasEngineMlock)

	chatSvc := chat.NewService(db, cfgStore, bc, skillsRegistry)
	chatSvc.SetEngineManager(engineMgr)
	chatSvc.SetRouterEngineManager(routerMgr)
	commsSvc := comms.New(cfgStore, db)
	forgeSvc := forge.NewService(config.SupportDir())
	platformHost := platform.NewHost(
		cfgStore,
		platform.NewSQLiteStorage(db),
		platform.NewChatAgentRuntime(chatSvc),
		platform.NoopContextAssembler{},
		platform.NewInProcessBus(256),
	)
	moduleRegistry := platform.NewModuleRegistry(platformHost)
	approvalsModule := approvalsmodule.New(config.SupportDir())
	if err := moduleRegistry.Register(approvalsModule); err != nil {
		log.Fatalf("Atlas: register approvals module: %v", err)
	}
	automationsModule := automationsmodule.New(config.SupportDir())
	automationsModule.SetDeliveryService(commsSvc)
	automationsModule.SetSkillRegistry(skillsRegistry)
	if err := moduleRegistry.Register(automationsModule); err != nil {
		log.Fatalf("Atlas: register automations module: %v", err)
	}
	communicationsModule := communicationsmodule.New(commsSvc)
	communicationsModule.SetSkillRegistry(skillsRegistry)
	if err := moduleRegistry.Register(communicationsModule); err != nil {
		log.Fatalf("Atlas: register communications module: %v", err)
	}
	forgeModule := forgemodule.New(config.SupportDir(), forgeSvc, chatSvc, skillsRegistry)
	if err := moduleRegistry.Register(forgeModule); err != nil {
		log.Fatalf("Atlas: register forge module: %v", err)
	}
	workflowsModule := workflowsmodule.New(config.SupportDir())
	workflowsModule.SetSkillRegistry(skillsRegistry)
	if err := moduleRegistry.Register(workflowsModule); err != nil {
		log.Fatalf("Atlas: register workflows module: %v", err)
	}
	dashboardsModule := dashboardsmodule.New(config.SupportDir(), dbPath)
	dashboardsModule.SetRuntimeFetcher(dashboardsmodule.NewLoopbackFetcher(port))
	dashboardsModule.SetSkillExecutor(skillsRegistry)
	dashboardsModule.SetProviderResolver(func() (agent.ProviderConfig, error) {
		return chat.ResolveProvider(cfgStore.Load())
	})
	if err := moduleRegistry.Register(dashboardsModule); err != nil {
		log.Fatalf("Atlas: register dashboards module: %v", err)
	}
	dashboardsModule.RegisterSkills(skillsRegistry)
	skillsModule := skillsmodule.New(config.SupportDir())
	if err := moduleRegistry.Register(skillsModule); err != nil {
		log.Fatalf("Atlas: register skills module: %v", err)
	}
	apiValidationModule := apivalidationmodule.New(config.SupportDir())
	if err := moduleRegistry.Register(apiValidationModule); err != nil {
		log.Fatalf("Atlas: register api validation module: %v", err)
	}
	engineModule := enginemodule.New(engineMgr, routerMgr, cfgStore)
	if err := moduleRegistry.Register(engineModule); err != nil {
		log.Fatalf("Atlas: register engine module: %v", err)
	}
	usageModule := usagemodule.New(db)
	if err := moduleRegistry.Register(usageModule); err != nil {
		log.Fatalf("Atlas: register usage module: %v", err)
	}
	voiceModule := voicemodule.New(voiceMgr, cfgStore)
	if err := moduleRegistry.Register(voiceModule); err != nil {
		log.Fatalf("Atlas: register voice module: %v", err)
	}
	communicationsModule.SetApprovalResolver(func(toolCallID string, approved bool) error {
		return approvalsModule.Resolve(toolCallID, approved)
	})
	communicationsModule.SetChatHandler(newBridgeChatHandler(chatSvc))
	if err := moduleRegistry.StartAll(context.Background()); err != nil {
		log.Fatalf("Atlas: start module registry: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := moduleRegistry.StopAll(stopCtx); err != nil {
			log.Printf("Atlas: module registry shutdown: %v", err)
		}
	}()

	// Wire forge.orchestration.propose → forge service.
	skillsRegistry.SetForgePersistFn(func(specJSON, plansJSON, summary, rationale, contractJSON string) (
		id, name, skillID, riskLevel string,
		actionNames, domains []string,
		err error,
	) {
		return forgeSvc.PersistProposalFromJSON(specJSON, plansJSON, summary, rationale, contractJSON)
	})

	// Wire approval resolver to Telegram bridge (allows inline approve/deny buttons).
	// Wire chat handler to comms bridges.
	// This is the single mapping point between comms.BridgeRequest and chat.MessageRequest.
	// When chat.MessageRequest gains a new field, add it to comms.BridgeRequest and map it here.

	// Dream cycle — nightly memory consolidation (prune, merge, diary synthesis, MIND refresh).
	// Uses the heavy background provider (cloud fast model by default; local router when
	// AtlasEngineRouterForAll is enabled) for quality-sensitive consolidation work.
	dreamStop := mind.StartDreamCycle(config.SupportDir(), db, cfgStore,
		func() (agent.ProviderConfig, error) {
			return chat.ResolveHeavyBackgroundProvider(cfgStore.Load())
		})
	defer dreamStop()

	// ── Web UI directory ──────────────────────────────────────────────────────
	webDir := *webDirFlag
	if webDir == "" {
		webDir = resolveWebDir()
	}
	if webDir != "" {
		log.Printf("Atlas: web UI at %s", webDir)
	} else {
		log.Printf("Atlas: web UI not found (use -web-dir to specify path)")
	}

	// ── Domain handlers ───────────────────────────────────────────────────────
	authDomain := domain.NewAuthDomain(authSvc, cfgStore, webDir, port)
	authDomain.EnsureRemoteKey() // Generate initial key if Keychain has none.
	controlDomain := domain.NewControlDomain(cfgStore, runtimeSvc, db, engineMgr)
	chatDomain := domain.NewChatDomain(chatSvc, bc, db)
	var approvalsDomain *domain.ApprovalsDomain
	var commsDomain *domain.CommunicationsDomain

	// Wire dream cycle force-trigger → POST /mind/dream.
	chatDomain.SetDreamRunner(func() {
		mind.RunDreamNow(config.SupportDir(), db, cfgStore, func() (agent.ProviderConfig, error) {
			return chat.ResolveHeavyBackgroundProvider(cfgStore.Load())
		})
	})

	// ── HTTP server ───────────────────────────────────────────────────────────
	remoteEnabled := func() bool { return cfgStore.Load().RemoteAccessEnabled }
	tailscaleEnabled := func() bool { return cfgStore.Load().TailscaleEnabled }

	handler := server.BuildRouter(
		authDomain,
		controlDomain,
		chatDomain,
		approvalsDomain,
		commsDomain,
		authSvc,
		remoteEnabled,
		tailscaleEnabled,
		platformHost,
	)

	addr := fmt.Sprintf("0.0.0.0:%d", port)

	runtimeSvc.MarkStarted()
	logstore.Write("info", fmt.Sprintf("Runtime started on port %d", port),
		map[string]string{"provider": cfg.ActiveAIProvider})
	log.Printf("Atlas: listening on http://%s", addr)
	log.Printf("Atlas: config at %s", config.ConfigPath())
	log.Printf("Atlas: database at %s", dbPath)
	log.Printf("Atlas: all domains native — no Swift backend required")

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("Atlas: server error: %v", err)
	}
}

// resolveWebDir attempts to find the atlas-web/dist directory relative to
// the binary or the current working directory.
func resolveWebDir() string {
	candidates := []string{
		filepath.Join(filepath.Dir(os.Args[0]), "web"),
		filepath.Join("Atlas", "atlas-web", "dist"),
		filepath.Join("..", "atlas-web", "dist"),
		filepath.Join("..", "..", "atlas-web", "dist"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(filepath.Join(p, "index.html")); err == nil {
			abs, _ := filepath.Abs(p)
			return abs
		}
	}
	return ""
}
