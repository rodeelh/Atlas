package platform

import (
	"sync"

	"github.com/go-chi/chi/v5"

	"atlas-runtime-go/internal/config"
)

// Host is the private runtime contract exposed to internal modules.
type Host interface {
	Bus() EventBus
	Config() ConfigReader
	Storage() Storage
	AgentRuntime() AgentRuntime
	ContextAssembler() ContextAssembler
	MountPublic(register func(chi.Router))
	MountProtected(register func(chi.Router))
}

// ConfigReader is the minimal config contract shared with modules.
type ConfigReader interface {
	Load() config.RuntimeConfigSnapshot
}

// RuntimeHost is the concrete private host implementation used by the runtime.
type RuntimeHost struct {
	bus              EventBus
	cfg              ConfigReader
	storage          Storage
	agentRuntime     AgentRuntime
	contextAssembler ContextAssembler

	mu              sync.RWMutex
	publicMounts    []func(chi.Router)
	protectedMounts []func(chi.Router)
}

func NewHost(cfg ConfigReader, storage Storage, agentRuntime AgentRuntime, contextAssembler ContextAssembler, bus EventBus) *RuntimeHost {
	if contextAssembler == nil {
		contextAssembler = NoopContextAssembler{}
	}
	if bus == nil {
		bus = NewInProcessBus(256)
	}
	return &RuntimeHost{
		bus:              bus,
		cfg:              cfg,
		storage:          storage,
		agentRuntime:     agentRuntime,
		contextAssembler: contextAssembler,
	}
}

func (h *RuntimeHost) Bus() EventBus { return h.bus }

func (h *RuntimeHost) Config() ConfigReader { return h.cfg }

func (h *RuntimeHost) Storage() Storage { return h.storage }

func (h *RuntimeHost) AgentRuntime() AgentRuntime { return h.agentRuntime }

func (h *RuntimeHost) ContextAssembler() ContextAssembler { return h.contextAssembler }

func (h *RuntimeHost) MountPublic(register func(chi.Router)) {
	if register == nil {
		return
	}
	h.mu.Lock()
	h.publicMounts = append(h.publicMounts, register)
	h.mu.Unlock()
}

func (h *RuntimeHost) MountProtected(register func(chi.Router)) {
	if register == nil {
		return
	}
	h.mu.Lock()
	h.protectedMounts = append(h.protectedMounts, register)
	h.mu.Unlock()
}

func (h *RuntimeHost) ApplyPublic(r chi.Router) {
	h.mu.RLock()
	mounts := append([]func(chi.Router){}, h.publicMounts...)
	h.mu.RUnlock()
	for _, mount := range mounts {
		mount(r)
	}
}

func (h *RuntimeHost) ApplyProtected(r chi.Router) {
	h.mu.RLock()
	mounts := append([]func(chi.Router){}, h.protectedMounts...)
	h.mu.RUnlock()
	for _, mount := range mounts {
		mount(r)
	}
}
