// Command Atlas is the Atlas Go runtime — the full replacement for AtlasRuntimeService.
// It serves the complete Atlas HTTP API natively in Go. No Swift backend is required.
//
//	Atlas [-port 1984] [-web-dir ./web]
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"atlas-runtime-go/internal/agent"
	"atlas-runtime-go/internal/auth"
	"atlas-runtime-go/internal/browser"
	"atlas-runtime-go/internal/chat"
	"atlas-runtime-go/internal/comms"
	"atlas-runtime-go/internal/config"
	"atlas-runtime-go/internal/domain"
	"atlas-runtime-go/internal/engine"
	"atlas-runtime-go/internal/features"
	"atlas-runtime-go/internal/forge"
	"atlas-runtime-go/internal/location"
	"atlas-runtime-go/internal/logstore"
	"atlas-runtime-go/internal/memory"
	"atlas-runtime-go/internal/mind"
	mindtelemetry "atlas-runtime-go/internal/mind/telemetry"
	apivalidationmodule "atlas-runtime-go/internal/modules/apivalidation"
	approvalsmodule "atlas-runtime-go/internal/modules/approvals"
	automationsmodule "atlas-runtime-go/internal/modules/automations"
	communicationsmodule "atlas-runtime-go/internal/modules/communications"
	dashboardsmodule "atlas-runtime-go/internal/modules/dashboards"
	enginemodule "atlas-runtime-go/internal/modules/engine"
	forgemodule "atlas-runtime-go/internal/modules/forge"
	mindmodule "atlas-runtime-go/internal/modules/mind"
	skillsmodule "atlas-runtime-go/internal/modules/skills"
	agentsmodule "atlas-runtime-go/internal/modules/agents"
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
	rootCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

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

	// Mind telemetry emitter — non-blocking sink for every event in the
	// mind-thoughts subsystem (naps, thought lifecycle, auto-execute,
	// approvals, greetings, sidebar). Writes are buffered and drained by a
	// background goroutine; Emit is safe to call from anywhere without
	// holding up the caller on disk I/O.
	mindTelemetry := mindtelemetry.New(db)
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = mindTelemetry.Stop(stopCtx)
	}()

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
	voiceMgr := voice.NewManager(config.AtlasInstallDir(), config.VoiceModelsDir(), cfgStore)
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

	// Embedding sidecar — llama-server in --embedding mode (nomic-embed-text-v1.5).
	// Runs on a dedicated port (default 11987) and is preferred over the chat provider's
	// embedding API when enabled. Allows Anthropic + local users to get memory embeddings.
	embedMgr := engine.NewEmbedManager(config.AtlasInstallDir(), config.ModelsDir())
	if cfg.AtlasEmbedEnabled {
		const embedModel = "nomic-embed-text-v1.5.Q4_K_M.gguf"
		embedPort := cfg.AtlasEmbedPort
		if embedPort <= 0 {
			embedPort = 11988
		}
		go func() {
			if err := embedMgr.Start(embedModel, embedPort); err != nil {
				logstore.Write("warn", "embed sidecar start failed", map[string]string{"error": err.Error()})
				return
			}
			if err := embedMgr.WaitUntilReady(embedPort, 60*time.Second); err != nil {
				logstore.Write("warn", "embed sidecar not ready", map[string]string{"error": err.Error()})
				return
			}
			agent.SetEmbedSidecarURL(embedMgr.BaseURL())
			logstore.Write("info", "embed sidecar ready", map[string]string{"url": embedMgr.BaseURL()})
		}()
	}

	engineMgr.SetIdleTimeout(60 * time.Minute) // eject primary model after 60 min idle
	routerMgr.SetIdleTimeout(12 * time.Hour)   // eject router model after 12 hr idle
	engineMgr.SetMlock(cfg.AtlasEngineMlock)   // pin model in RAM (configurable via UI)
	routerMgr.SetMlock(cfg.AtlasEngineMlock)

	// MLX-LM subsystem — Apple Silicon only. Two MLXManager instances mirror the
	// llama.cpp pair: primary (port 11990) + MLX-exclusive router (port 11991).
	// Both are always constructed; Start() returns early with a descriptive error
	// when called on a non-Apple-Silicon machine.
	mlxMgr := engine.NewMLXManager(config.MLXVenvDir(), config.MLXModelsDir())
	mlxRouterMgr := engine.NewMLXManager(config.MLXVenvDir(), config.MLXModelsDir())
	mlxMgr.SetIdleTimeout(60 * time.Minute)
	mlxRouterMgr.SetIdleTimeout(12 * time.Hour)

	chatSvc := chat.NewService(db, cfgStore, bc, skillsRegistry)
	chatSvc.SetEngineManager(engineMgr)
	chatSvc.SetRouterEngineManager(routerMgr)
	chatSvc.SetMLXEngineManager(mlxMgr)
	chatSvc.SetMLXRouterEngineManager(mlxRouterMgr)

	// Post-turn hooks — registered here so chat/pipeline.go has no direct
	// import of memory or mind.
	chatSvc.RegisterHook(func(ctx context.Context, rec chat.TurnRecord) {
		go memory.ExtractAndPersist(ctx, rec.Cfg, rec.HeavyBgProvider,
			rec.UserMessage, rec.AssistantResponse,
			rec.ToolCallSummaries, rec.ToolResultSummaries,
			rec.ConvID, db)
	})
	chatSvc.RegisterHook(func(ctx context.Context, rec chat.TurnRecord) {
		if rec.AssistantResponse == "" {
			return
		}
		turn := mind.TurnRecord{
			ConversationID:      rec.ConvID,
			UserMessage:         rec.UserMessage,
			AssistantResponse:   rec.AssistantResponse,
			ToolCallSummaries:   rec.ToolCallSummaries,
			ToolResultSummaries: rec.ToolResultSummaries,
			Timestamp:           time.Now(),
		}
		// ReflectNonBlocking and LearnFromTurnNonBlocking each spawn their own
		// goroutine internally; call them directly here without wrapping.
		mind.ReflectNonBlocking(rec.HeavyBgProvider, turn, config.SupportDir())
		mind.LearnFromTurnNonBlocking(rec.HeavyBgProvider, turn, config.SupportDir())
	})
	chatSvc.RegisterHook(func(_ context.Context, _ chat.TurnRecord) {
		mind.NotifyTurnNonBlocking()
	})

	commsSvc := comms.New(cfgStore, db)
	commsSvc.SetWebChatSender(chatSvc.InjectAssistantMessage)
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
	// Wire local Whisper transcription into the Telegram bridge so voice messages
	// are automatically converted to text before reaching the agent.
	// The bundled whisper-server only accepts WAV, so non-WAV audio (e.g. the
	// OGG Opus that Telegram sends for voice messages) is converted first via ffmpeg.
	communicationsModule.SetTranscriber(func(ctx context.Context, data []byte, mimeType string) (string, error) {
		c := cfgStore.Load()
		model := c.VoiceWhisperModel
		if model == "" {
			model = "ggml-base.en.bin"
		}
		port := c.VoiceWhisperPort
		if port == 0 {
			port = 11987
		}

		// Convert to WAV if needed — whisper-server is compiled without ffmpeg support.
		wavData, err := voice.ConvertToWAV(ctx, data, mimeType)
		if err != nil {
			return "", fmt.Errorf("audio conversion: %w", err)
		}
		wavMime := "audio/wav"

		result, err := voiceMgr.Transcribe(ctx, wavData, wavMime, c.VoiceWhisperLanguage)
		if err != nil {
			return "", err
		}
		return result.Text, nil
	})
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
	dashboardsModule.SetDatabase(db.Conn())
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
	agentsModule := agentsmodule.New(config.SupportDir())
	agentsModule.SetSkillRegistry(skillsRegistry)
	agentsModule.SetDatabase(db)
	if err := moduleRegistry.Register(agentsModule); err != nil {
		log.Fatalf("Atlas: register agents module: %v", err)
	}
	apiValidationModule := apivalidationmodule.New(config.SupportDir())
	if err := moduleRegistry.Register(apiValidationModule); err != nil {
		log.Fatalf("Atlas: register api validation module: %v", err)
	}
	engineModule := enginemodule.New(engineMgr, routerMgr, cfgStore).WithMLX(mlxMgr, mlxRouterMgr).WithEmbed(embedMgr)
	if err := moduleRegistry.Register(engineModule); err != nil {
		log.Fatalf("Atlas: register engine module: %v", err)
	}
	usageModule := usagemodule.New(db)
	if err := moduleRegistry.Register(usageModule); err != nil {
		log.Fatalf("Atlas: register usage module: %v", err)
	}
	mindModule := mindmodule.New(config.SupportDir(), db, cfgStore, mindTelemetry)
	mindModule.SetProviderResolver(func() (agent.ProviderConfig, error) {
		return chat.ResolveHeavyBackgroundProvider(cfgStore.Load())
	})
	mindModule.SetSkillsLister(func() []mind.SkillLine {
		records := features.ListSkills(config.SupportDir())
		out := make([]mind.SkillLine, 0, len(records))
		for _, rec := range records {
			if rec.Manifest.LifecycleState == "uninstalled" {
				continue
			}
			// One entry per skill id (not per action) to keep the nap
			// prompt concise. The description is the skill-level one.
			out = append(out, mind.SkillLine{
				ID:          rec.Manifest.ID,
				Description: rec.Manifest.Description,
			})
		}
		return out
	})
	if err := moduleRegistry.Register(mindModule); err != nil {
		log.Fatalf("Atlas: register mind module: %v", err)
	}
	// mind module needs the dispatcher for the manual POST /mind/nap path.
	// Dispatcher is built a few lines below the scheduler — so we set it
	// after both exist via a small deferred wiring hook.
	voiceModule := voicemodule.New(voiceMgr, cfgStore)
	if err := moduleRegistry.Register(voiceModule); err != nil {
		log.Fatalf("Atlas: register voice module: %v", err)
	}
	communicationsModule.SetApprovalResolver(func(toolCallID string, approved bool) error {
		return approvalsModule.Resolve(toolCallID, approved)
	})
	communicationsModule.SetChatHandler(newBridgeChatHandler(chatSvc))
	if err := moduleRegistry.StartAll(rootCtx); err != nil {
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

	// Mind dispatcher — the action gate for the thoughts subsystem. Wires
	// the skills registry into the dispatcher via a narrow adapter so the
	// mind package doesn't import internal/skills directly. Phase 5 wires
	// the greeting queuer so acted-on thoughts flow into the live chat
	// greeting instead of just piling up in pending-greetings.json.
	napDispatcher := mind.NewDispatcher(
		config.SupportDir(),
		newMindSkillExecutor(skillsRegistry),
		approvalsModule, // ApprovalProposer — phase 6 wires the thought-sourced approval path
		chat.NewChatGreetingQueuer(config.SupportDir()),
		mindTelemetry,
	)
	// Phase 6: when a thought-sourced approval is approved by the user, the
	// approvals module calls this resolver to run the skill and enqueue the
	// result to the greeting queue, same as the auto-execute path.
	approvalsModule.SetThoughtResolver(func(thoughtID, skillID string, args json.RawMessage) (string, error) {
		execCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		res, err := skillsRegistry.Execute(execCtx, skillID, args)
		if err != nil {
			return "", err
		}
		resultText := res.FormatForModel()
		_ = chat.NewChatGreetingQueuer(config.SupportDir()).EnqueueGreeting(mind.GreetingEntry{
			ThoughtID:  thoughtID,
			SkillID:    skillID,
			Result:     resultText,
			ExecutedAt: time.Now().UTC(),
		})
		mindTelemetry.Emit("approval_resolved", thoughtID, "", map[string]any{
			"skill":      skillID,
			"result_len": len(resultText),
		})
		return resultText, nil
	})
	// Phase 5: wire the greeting telemetry sink into chat.Service so the
	// HandleGreeting path emits greeting_delivered / greeting_skipped rows.
	chatSvc.SetGreetingTelemetry(mindTelemetry)

	// Nap scheduler — idle + floor triggers for the mind-thoughts subsystem.
	// Ships dormant (cfg.NapsEnabled=false by default). Flip NapsEnabled in
	// the runtime config to turn it on without a restart.
	napScheduler := mind.NewScheduler(
		config.SupportDir(), db, cfgStore, mindTelemetry,
		func() (agent.ProviderConfig, error) {
			return chat.ResolveHeavyBackgroundProvider(cfgStore.Load())
		},
		mind.BuildSkillsLister(config.SupportDir()),
	)
	napScheduler.SetDispatcher(napDispatcher)
	mindModule.SetDispatcher(napDispatcher)
	napScheduler.Start(rootCtx)
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		napScheduler.Stop(stopCtx)
	}()

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

	localAuthSvc, err := auth.NewLocalAuthService(db, port)
	if err != nil {
		log.Fatalf("Atlas: local auth init: %v", err)
	}
	// Wire the LocalAuthService into the session Service so HasLocalCredentials
	// uses the atomic flag (no DB round-trip, no TOCTOU window).
	authSvc.SetLocalAuth(localAuthSvc)
	localAuthDomain := domain.NewLocalAuthDomain(authSvc, localAuthSvc)
	authDomain.SetLocalAuth(localAuthSvc)

	controlDomain := domain.NewControlDomain(cfgStore, runtimeSvc, db, engineMgr)
	controlDomain.SetMLXManager(mlxMgr)
	chatDomain := domain.NewChatDomain(chatSvc, bc, db)
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
		localAuthDomain,
		controlDomain,
		chatDomain,
		authSvc,
		runtimeSvc,
		remoteEnabled,
		tailscaleEnabled,
		platformHost,
	)

	// Always bind to all interfaces so that enabling remote access or Tailscale
	// in the config takes effect immediately at the next request — no daemon
	// restart required. LanGate and RequireSession middleware enforce all
	// access-control policy at request time; a restrictive bind address would
	// silently break the toggle without giving the user a clear error.
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	tlsAssets, err := server.EnsureTLSAssets()
	if err != nil {
		log.Fatalf("Atlas: prepare tls assets: %v", err)
	}
	baseListener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Atlas: listen: %v", err)
	}
	protocolMux := server.NewProtocolMux(baseListener)
	defer protocolMux.Close()

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           server.UpgradeRemotePlainHTTP(handler),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	httpsSrv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{tlsAssets.Cert},
		},
	}

	runtimeSvc.MarkStarted()
	logstore.Write("info", fmt.Sprintf("Runtime started on port %d", port),
		map[string]string{"provider": cfg.ActiveAIProvider})
	log.Printf("Atlas: listening on http://%s (localhost/plaintext)", addr)
	log.Printf("Atlas: listening on https://%s (remote LAN)", addr)
	log.Printf("Atlas: tls certificate at %s", tlsAssets.CertPath)
	log.Printf("Atlas: config at %s", config.ConfigPath())
	log.Printf("Atlas: database at %s", dbPath)
	log.Printf("Atlas: all domains native — no Swift backend required")

	serverErr := make(chan error, 2)
	go func() {
		serverErr <- httpSrv.Serve(protocolMux.HTTPListener())
	}()
	go func() {
		serverErr <- httpsSrv.Serve(tls.NewListener(protocolMux.TLSListener(), httpsSrv.TLSConfig))
	}()

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runtimeSvc.RecordError("server error: " + err.Error())
			log.Fatalf("Atlas: server error: %v", err)
		}
	case <-rootCtx.Done():
		log.Printf("Atlas: shutdown requested")
	}

	runtimeSvc.MarkStopped()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	protocolMux.Close()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		runtimeSvc.RecordError("shutdown error: " + err.Error())
		log.Printf("Atlas: server shutdown: %v", err)
	}
	if err := httpsSrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		runtimeSvc.RecordError("shutdown error: " + err.Error())
		log.Printf("Atlas: tls server shutdown: %v", err)
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
