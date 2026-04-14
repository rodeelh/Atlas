.PHONY: build build-web build-tui install-web test \
        install uninstall \
        run run-tui dev \
        daemon-start daemon-stop daemon-restart daemon-status daemon-logs \
        download-engine engine-update \
        download-whisper download-whisper-model \
        download-voice-venv download-kokoro download-kokoro-model download-voice \
        tidy clean check bump \
        test-fast test-standard verify-release scorecard benchmark-chat

RUNTIME_DIR  := atlas-runtime
WEB_DIR      := atlas-web
TUI_DIR      := atlas-tui

BINARY       := Atlas
DAEMON_LABEL := Atlas
PLIST_TMPL   := $(RUNTIME_DIR)/com.atlas.runtime.plist.tmpl

# ── Atlas Engine LM ───────────────────────────────────────────────────────────
# Pinned llama.cpp release. Override with: make install LLAMA_VERSION=bXXXX
LLAMA_VERSION ?= b8641

# ── Voice: Whisper STT (Phase 1) ─────────────────────────────────────────────
# Pinned whisper.cpp release. whisper.cpp does not ship prebuilt macOS binaries,
# so we clone the tagged source and build whisper-server with make. Mirrors
# the llama.cpp "download → install" shape: one make target, idempotent, safe
# to rerun. Override with: make install WHISPER_VERSION=vX.Y.Z
WHISPER_VERSION ?= v1.8.4
# Pinned default Whisper model — base.en is small (~142 MB) and English-only,
# a good default for interactive dictation. Users can download more models
# from the UI later. Override with: make install WHISPER_MODEL=ggml-small.en.bin
WHISPER_MODEL ?= ggml-base.en.bin

# ── Voice: Kokoro TTS ────────────────────────────────────────────────────────
# Kokoro is installed via pip (kokoro-onnx package) into a dedicated venv at
# ~/Library/Application Support/Atlas/voice/venv. Model files are downloaded
# separately as fixed artifacts.
KOKORO_PIP_VERSION ?= 0.4.7

# ── Build ─────────────────────────────────────────────────────────────────────

build:
	cd $(RUNTIME_DIR) && go build -o $(BINARY) ./cmd/atlas-runtime

build-tui:
	cd $(TUI_DIR) && go build -o atlas-tui .

build-web:
	cd $(WEB_DIR) && npm run build

install-web: build-web
	@echo "→ Syncing web assets only..."
	@mkdir -p "$$HOME/Library/Application Support/Atlas/web"
	rsync -a --delete $(WEB_DIR)/dist/ "$$HOME/Library/Application Support/Atlas/web/"
	@echo "✓ Web UI updated. Refresh the Atlas window to see changes."

test:
	cd $(RUNTIME_DIR) && go test ./...

tidy:
	cd $(RUNTIME_DIR) && go mod tidy
	cd $(TUI_DIR) && go mod tidy

clean:
	rm -f $(RUNTIME_DIR)/$(BINARY)
	rm -f $(TUI_DIR)/atlas-tui

# ── Run (dev) ─────────────────────────────────────────────────────────────────

run: build
	$(RUNTIME_DIR)/$(BINARY) -web-dir $(WEB_DIR)/dist

run-tui: build-tui
	$(TUI_DIR)/atlas-tui

dev: build
	$(RUNTIME_DIR)/$(BINARY) -port 1985 -web-dir $(WEB_DIR)/dist

# ── Daemon ────────────────────────────────────────────────────────────────────
#
# install  — build all components, deploy to ~/Library/Application Support/Atlas/,
#            write plist to ~/Library/LaunchAgents/, load daemon (idempotent).
# uninstall — unload daemon, remove plist and installed files (data preserved).

download-engine:
	@mkdir -p "$$HOME/Library/Application Support/Atlas/engine"
	@if [ ! -f "$$HOME/Library/Application Support/Atlas/engine/llama-server" ]; then \
		echo "→ Downloading llama-server $(LLAMA_VERSION) for $$(uname -m)..."; \
		ARCH=$$(uname -m); \
		ZIP="llama-$(LLAMA_VERSION)-bin-macos-$$ARCH.zip"; \
		URL="https://github.com/ggerganov/llama.cpp/releases/download/$(LLAMA_VERSION)/$$ZIP"; \
		curl -L --progress-bar -o /tmp/llama-engine.tar.gz "$$URL" || { echo "✗ llama-server download failed — Engine LM will not be available"; rm -f /tmp/llama-engine.tar.gz; exit 0; }; \
		mkdir -p /tmp/llama-extract && \
		tar -xzf /tmp/llama-engine.tar.gz -C /tmp/llama-extract 2>/dev/null; \
		EXTRACTED=$$(ls /tmp/llama-extract/); \
		cp /tmp/llama-extract/$$EXTRACTED/llama-server "$$HOME/Library/Application Support/Atlas/engine/llama-server" || \
			{ echo "✗ Could not extract llama-server from archive"; rm -rf /tmp/llama-extract /tmp/llama-engine.tar.gz; exit 0; }; \
		cp /tmp/llama-extract/$$EXTRACTED/*.dylib "$$HOME/Library/Application Support/Atlas/engine/" 2>/dev/null; \
		chmod +x "$$HOME/Library/Application Support/Atlas/engine/llama-server"; \
		rm -rf /tmp/llama-extract /tmp/llama-engine.tar.gz; \
		echo "✓ llama-server $(LLAMA_VERSION) + shared libraries ready"; \
	else \
		echo "→ llama-server already installed ($(LLAMA_VERSION)) — use 'make engine-update' to upgrade"; \
	fi

download-whisper:
	@mkdir -p "$$HOME/Library/Application Support/Atlas/voice"
	@if [ ! -f "$$HOME/Library/Application Support/Atlas/voice/whisper-server" ]; then \
		echo "→ Building whisper-server $(WHISPER_VERSION) for $$(uname -m)..."; \
		SRC=/tmp/atlas-whisper-src; \
		rm -rf $$SRC; \
		git clone --depth 1 --branch $(WHISPER_VERSION) https://github.com/ggml-org/whisper.cpp.git $$SRC 2>&1 | tail -3 || { echo "✗ whisper.cpp clone failed — voice STT will not be available"; exit 0; }; \
		(cd $$SRC && cmake -B build -DCMAKE_BUILD_TYPE=Release -DWHISPER_BUILD_EXAMPLES=ON >/dev/null 2>&1) || { echo "✗ whisper.cpp cmake configure failed"; exit 0; }; \
		(cd $$SRC && (cmake --build build --target whisper-server -j --config Release 2>/dev/null || cmake --build build --target server -j --config Release) 2>&1 | tail -5) || { echo "✗ whisper.cpp build failed"; exit 0; }; \
		SERVER_BIN=$$(find $$SRC/build -type f \( -name "whisper-server" -o -name "server" \) -perm -u+x 2>/dev/null | head -1); \
		if [ -z "$$SERVER_BIN" ]; then echo "✗ whisper server binary not found after build"; exit 0; fi; \
		cp "$$SERVER_BIN" "$$HOME/Library/Application Support/Atlas/voice/whisper-server"; \
		chmod +x "$$HOME/Library/Application Support/Atlas/voice/whisper-server"; \
		find $$SRC/build -type f \( -name "*.dylib" -o -name "*.so" \) -exec cp {} "$$HOME/Library/Application Support/Atlas/voice/" \; 2>/dev/null; \
		VOICE_DIR="$$HOME/Library/Application Support/Atlas/voice"; \
		(cd "$$VOICE_DIR" && for f in lib*.dylib; do \
			[ -f "$$f" ] || continue; \
			case "$$f" in *.*.*.*.dylib) ;; *) continue ;; esac; \
			base=$$(echo "$$f" | sed 's/\.[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\.dylib$$//'); \
			major=$$(echo "$$f" | sed -E 's/^lib[^.]+\.([0-9]+)\..*/\1/'); \
			ln -sf "$$f" "$${base}.$${major}.dylib" 2>/dev/null || true; \
			ln -sf "$$f" "$${base}.dylib" 2>/dev/null || true; \
		done); \
		install_name_tool -add_rpath "@executable_path/." "$$VOICE_DIR/whisper-server" 2>/dev/null || true; \
		codesign --force --sign - "$$VOICE_DIR/whisper-server" 2>/dev/null || true; \
		rm -rf $$SRC; \
		echo "✓ whisper-server $(WHISPER_VERSION) ready"; \
	else \
		echo "→ whisper-server already installed"; \
	fi

download-whisper-model:
	@mkdir -p "$$HOME/Library/Application Support/ProjectAtlas/voice-models/whisper"
	@if [ ! -f "$$HOME/Library/Application Support/ProjectAtlas/voice-models/whisper/$(WHISPER_MODEL)" ]; then \
		echo "→ Downloading Whisper model $(WHISPER_MODEL)..."; \
		curl -L --progress-bar \
			-o "$$HOME/Library/Application Support/ProjectAtlas/voice-models/whisper/$(WHISPER_MODEL)" \
			"https://huggingface.co/ggerganov/whisper.cpp/resolve/main/$(WHISPER_MODEL)" || \
			{ echo "✗ Whisper model download failed"; rm -f "$$HOME/Library/Application Support/ProjectAtlas/voice-models/whisper/$(WHISPER_MODEL)"; exit 0; }; \
		echo "✓ $(WHISPER_MODEL) ready"; \
	else \
		echo "→ Whisper model $(WHISPER_MODEL) already present"; \
	fi

download-voice-venv:
	@VOICE_DIR="$$HOME/Library/Application Support/Atlas/voice"; \
	VENV_DIR="$$VOICE_DIR/venv"; \
	mkdir -p "$$VOICE_DIR"; \
	if [ -x "$$VENV_DIR/bin/python" ]; then \
		echo "→ voice venv already exists at $$VENV_DIR"; \
		exit 0; \
	fi; \
	if ! command -v python3 >/dev/null 2>&1; then \
		echo "✗ python3 not found — install Xcode command line tools or run 'brew install python'"; \
		exit 0; \
	fi; \
	echo "→ Creating voice Python venv at $$VENV_DIR..."; \
	python3 -m venv "$$VENV_DIR" 2>&1 || { echo "✗ venv creation failed"; exit 0; }; \
	"$$VENV_DIR/bin/pip" install --quiet --upgrade pip 2>&1 | tail -3; \
	echo "✓ voice venv ready"


download-kokoro: download-voice-venv
	@VENV_DIR="$$HOME/Library/Application Support/Atlas/voice/venv"; \
	if "$$VENV_DIR/bin/python" -c "import kokoro_onnx" 2>/dev/null; then \
		echo "→ kokoro-onnx already installed in voice venv"; \
		exit 0; \
	fi; \
	echo "→ Installing kokoro-onnx $(KOKORO_PIP_VERSION) into voice venv..."; \
	"$$VENV_DIR/bin/pip" install --quiet "kokoro-onnx==$(KOKORO_PIP_VERSION)" 2>&1 | tail -5 || { \
		echo "✗ kokoro-onnx pip install failed"; \
		exit 0; \
	}; \
	echo "✓ kokoro-onnx $(KOKORO_PIP_VERSION) installed"

download-kokoro-model:
	@MODEL_DIR="$$HOME/Library/Application Support/ProjectAtlas/voice-models/kokoro"; \
	mkdir -p "$$MODEL_DIR"; \
	if [ ! -f "$$MODEL_DIR/kokoro-v1.0.onnx" ]; then \
		echo "→ Downloading Kokoro model (kokoro-v1.0.onnx, ~325 MB)..."; \
		curl -L --progress-bar \
			-o "$$MODEL_DIR/kokoro-v1.0.onnx" \
			"https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/kokoro-v1.0.onnx" || \
			{ echo "✗ kokoro model download failed"; rm -f "$$MODEL_DIR/kokoro-v1.0.onnx"; exit 0; }; \
		echo "✓ kokoro-v1.0.onnx ready"; \
	else \
		echo "→ kokoro-v1.0.onnx already present"; \
	fi; \
	if [ ! -f "$$MODEL_DIR/voices-v1.0.bin" ]; then \
		echo "→ Downloading Kokoro voices (voices-v1.0.bin, ~27 MB)..."; \
		curl -L --progress-bar \
			-o "$$MODEL_DIR/voices-v1.0.bin" \
			"https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/voices-v1.0.bin" || \
			{ echo "✗ kokoro voices download failed"; rm -f "$$MODEL_DIR/voices-v1.0.bin"; exit 0; }; \
		echo "✓ voices-v1.0.bin ready"; \
	else \
		echo "→ voices-v1.0.bin already present"; \
	fi

download-voice: download-whisper download-whisper-model download-kokoro download-kokoro-model

engine-update:
	@echo "→ Removing existing llama-server and shared libraries..."
	@rm -f "$$HOME/Library/Application Support/Atlas/engine/llama-server"
	@rm -f "$$HOME/Library/Application Support/Atlas/engine/"*.dylib
	@$(MAKE) download-engine LLAMA_VERSION=$(LLAMA_VERSION)

install: build build-tui build-web download-engine download-voice
	@echo "→ Installing runtime binary and web assets..."
	@mkdir -p "$$HOME/Library/Application Support/Atlas"
	cp $(RUNTIME_DIR)/$(BINARY) "$$HOME/Library/Application Support/Atlas/$(BINARY)"
	rsync -a --delete $(WEB_DIR)/dist/ "$$HOME/Library/Application Support/Atlas/web/"
	@echo "→ Installing TUI binary to ~/.local/bin..."
	@mkdir -p "$$HOME/.local/bin"
	cp $(TUI_DIR)/atlas-tui "$$HOME/.local/bin/atlas"
	@echo "✓ TUI installed — run: atlas"
	@echo "  (ensure ~/.local/bin is in your PATH)"
	@echo "→ Creating log directory..."
	@mkdir -p "$$HOME/Library/Logs/Atlas"
	@echo "→ Installing plist..."
	@mkdir -p "$$HOME/Library/LaunchAgents"
	sed "s|__HOME__|$$HOME|g" $(PLIST_TMPL) \
		> "$$HOME/Library/LaunchAgents/$(DAEMON_LABEL).plist"
	@echo "→ Stopping any running Atlas process on port 1984..."
	@-lsof -ti tcp:1984 | xargs kill 2>/dev/null; true
	@echo "→ Loading daemon (unloading first if already loaded)..."
	@-launchctl unload "$$HOME/Library/LaunchAgents/$(DAEMON_LABEL).plist" 2>/dev/null; true
	launchctl load -w "$$HOME/Library/LaunchAgents/$(DAEMON_LABEL).plist"
	@echo "✓ Atlas daemon installed and running on port 1984"

uninstall:
	@echo "→ Unloading daemon..."
	@-launchctl unload -w "$$HOME/Library/LaunchAgents/$(DAEMON_LABEL).plist" 2>/dev/null; true
	@-rm -f "$$HOME/Library/LaunchAgents/$(DAEMON_LABEL).plist"
	@echo "→ Removing installed files..."
	@-rm -f "$$HOME/Library/Application Support/Atlas/$(BINARY)"
	@-rm -rf "$$HOME/Library/Application Support/Atlas/web"
	@-rm -f "$$HOME/.local/bin/atlas"
	@echo "✓ Uninstalled (data in ~/Library/Application Support/ProjectAtlas preserved)"

daemon-start:
	launchctl start $(DAEMON_LABEL)

daemon-stop:
	launchctl stop $(DAEMON_LABEL)

daemon-restart: daemon-stop daemon-start

daemon-status:
	launchctl print gui/$$(id -u)/$(DAEMON_LABEL)

daemon-logs:
	tail -f "$$HOME/Library/Logs/Atlas/runtime.log"

# ── Lint ──────────────────────────────────────────────────────────────────────

check:
	cd $(RUNTIME_DIR) && go fmt ./... && go vet ./...
	cd $(TUI_DIR) && go fmt ./... && go vet ./...

benchmark-chat:
	./scripts/benchmark-chat.sh

# ── Tiered testing & release validation ─────────────────────────────────────
# See docs/testing/README.md and scripts/verify-release.sh.

test-fast:
	@./scripts/verify-release.sh fast

test-standard:
	@./scripts/verify-release.sh standard

verify-release:
	@./scripts/verify-release.sh release

scorecard: verify-release
	@echo "→ docs/testing/atlas-test-scorecard.md"

# ── Version bump ─────────────────────────────────────────────────────────────

bump:
	@cd $(WEB_DIR) && npm version patch --no-git-tag-version
	@echo "→ Version bumped. Run 'make install' to deploy."
