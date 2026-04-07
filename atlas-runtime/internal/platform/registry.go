package platform

import (
	"context"
	"fmt"
)

// ModuleRegistry owns registration and lifecycle ordering for internal modules.
type ModuleRegistry struct {
	host    Host
	modules map[string]Module
	order   []string
	started []Module
}

func NewModuleRegistry(host Host) *ModuleRegistry {
	return &ModuleRegistry{
		host:    host,
		modules: make(map[string]Module),
	}
}

func (r *ModuleRegistry) Register(module Module) error {
	if module == nil {
		return fmt.Errorf("platform: module is nil")
	}
	id := module.ID()
	if id == "" {
		return fmt.Errorf("platform: module id is required")
	}
	if _, exists := r.modules[id]; exists {
		return fmt.Errorf("platform: module %q already registered", id)
	}
	if err := module.Register(r.host); err != nil {
		return fmt.Errorf("platform: register %s: %w", id, err)
	}
	r.modules[id] = module
	r.order = append(r.order, id)
	return nil
}

func (r *ModuleRegistry) StartAll(ctx context.Context) error {
	ordered, err := r.orderModules()
	if err != nil {
		return err
	}

	r.started = r.started[:0]
	for _, module := range ordered {
		if err := module.Start(ctx); err != nil {
			return fmt.Errorf("platform: start %s: %w", module.ID(), err)
		}
		r.started = append(r.started, module)
	}
	return nil
}

func (r *ModuleRegistry) StopAll(ctx context.Context) error {
	var firstErr error
	for i := len(r.started) - 1; i >= 0; i-- {
		if err := r.started[i].Stop(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("platform: stop %s: %w", r.started[i].ID(), err)
		}
	}
	r.started = r.started[:0]
	return firstErr
}

func (r *ModuleRegistry) orderModules() ([]Module, error) {
	ordered := make([]Module, 0, len(r.modules))
	visiting := make(map[string]bool)
	visited := make(map[string]bool)

	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("platform: cyclic dependency at %s", id)
		}
		module, ok := r.modules[id]
		if !ok {
			return fmt.Errorf("platform: unknown module %s", id)
		}
		visiting[id] = true
		for _, dep := range module.Manifest().Requires {
			if _, exists := r.modules[dep]; !exists {
				return fmt.Errorf("platform: module %s requires unknown module %s", id, dep)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		ordered = append(ordered, module)
		return nil
	}

	for _, id := range r.order {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}
