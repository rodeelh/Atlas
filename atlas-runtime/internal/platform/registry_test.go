package platform

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

type testModule struct {
	id       string
	manifest Manifest
	order    *[]string
	startErr error
	stopErr  error
}

func (m testModule) ID() string { return m.id }

func (m testModule) Manifest() Manifest { return m.manifest }

func (m testModule) Register(Host) error {
	*m.order = append(*m.order, "register:"+m.id)
	return nil
}

func (m testModule) Start(context.Context) error {
	*m.order = append(*m.order, "start:"+m.id)
	return m.startErr
}

func (m testModule) Stop(context.Context) error {
	*m.order = append(*m.order, "stop:"+m.id)
	return m.stopErr
}

type noopConfigReader struct{}

func (noopConfigReader) Load() struct{} { return struct{}{} }

func TestModuleRegistry_StartsInDependencyOrder(t *testing.T) {
	order := []string{}
	host := NewHost(nil, nil, nil, NoopContextAssembler{}, nil)
	registry := NewModuleRegistry(host)

	modules := []testModule{
		{id: "comms", manifest: Manifest{Requires: []string{"approvals"}}, order: &order},
		{id: "approvals", order: &order},
		{id: "automations", manifest: Manifest{Requires: []string{"approvals"}}, order: &order},
	}
	for _, module := range modules {
		if err := registry.Register(module); err != nil {
			t.Fatalf("Register(%s): %v", module.id, err)
		}
	}

	if err := registry.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll: %v", err)
	}

	gotStarts := []string{}
	for _, step := range order {
		if len(step) >= 6 && step[:6] == "start:" {
			gotStarts = append(gotStarts, step)
		}
	}
	wantStarts := []string{"start:approvals", "start:comms", "start:automations"}
	if !reflect.DeepEqual(gotStarts, wantStarts) {
		t.Fatalf("start order mismatch\nwant: %v\ngot:  %v", wantStarts, gotStarts)
	}
}

func TestModuleRegistry_RejectsUnknownDependencyOnStart(t *testing.T) {
	order := []string{}
	host := NewHost(nil, nil, nil, NoopContextAssembler{}, nil)
	registry := NewModuleRegistry(host)

	if err := registry.Register(testModule{
		id:       "comms",
		manifest: Manifest{Requires: []string{"missing"}},
		order:    &order,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := registry.StartAll(context.Background()); err == nil {
		t.Fatal("expected StartAll error for unknown dependency")
	}
}

func TestModuleRegistry_StopAll_ReversesStartedOrder(t *testing.T) {
	order := []string{}
	host := NewHost(nil, nil, nil, NoopContextAssembler{}, nil)
	registry := NewModuleRegistry(host)

	for _, module := range []testModule{
		{id: "approvals", order: &order},
		{id: "automations", manifest: Manifest{Requires: []string{"approvals"}}, order: &order},
	} {
		if err := registry.Register(module); err != nil {
			t.Fatalf("Register(%s): %v", module.id, err)
		}
	}

	if err := registry.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	if err := registry.StopAll(context.Background()); err != nil {
		t.Fatalf("StopAll: %v", err)
	}

	gotStops := []string{}
	for _, step := range order {
		if len(step) >= 5 && step[:5] == "stop:" {
			gotStops = append(gotStops, step)
		}
	}
	wantStops := []string{"stop:automations", "stop:approvals"}
	if !reflect.DeepEqual(gotStops, wantStops) {
		t.Fatalf("stop order mismatch\nwant: %v\ngot:  %v", wantStops, gotStops)
	}
}

func TestModuleRegistry_StartAllStopsAtFirstError(t *testing.T) {
	order := []string{}
	host := NewHost(nil, nil, nil, NoopContextAssembler{}, nil)
	registry := NewModuleRegistry(host)

	for _, module := range []testModule{
		{id: "approvals", order: &order},
		{id: "automations", manifest: Manifest{Requires: []string{"approvals"}}, order: &order, startErr: fmt.Errorf("boom")},
		{id: "comms", manifest: Manifest{Requires: []string{"automations"}}, order: &order},
	} {
		if err := registry.Register(module); err != nil {
			t.Fatalf("Register(%s): %v", module.id, err)
		}
	}

	if err := registry.StartAll(context.Background()); err == nil {
		t.Fatal("expected StartAll error")
	}

	for _, step := range order {
		if step == "start:comms" {
			t.Fatal("comms should not start after prior failure")
		}
	}
}
