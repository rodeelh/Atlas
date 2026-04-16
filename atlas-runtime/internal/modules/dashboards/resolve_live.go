package dashboards

// resolve_live.go — runs a live_compute source: a small AI call that takes
// the outputs of other sources as inputs and emits a JSON payload shaped by
// the declared output schema. The resolver itself is transport-agnostic; the
// actual AI invocation is delegated to a LiveComputeRunner wired by the
// module.

import (
	"context"
	"errors"
	"fmt"
)

// resolveLiveCompute gathers the named inputs, invokes the runner, and
// returns whatever the runner produced.
// cfg shape:
//
//	{
//	  "prompt":       "summarize the feed into {title, bullets:[]}",
//	  "inputs":       ["feed_source", "metric_source"],
//	  "outputSchema": { ... JSON schema ... }
//	}
func resolveLiveCompute(ctx context.Context, deps resolverDeps, cfg map[string]any, otherSources map[string]any) (any, error) {
	if err := validateLiveCompute(cfg); err != nil {
		return nil, err
	}
	if deps.liveRunner == nil {
		return nil, errors.New("live_compute: runner not wired")
	}

	spec := LiveComputeSpec{
		Prompt: cfg["prompt"].(string),
	}
	if raw, ok := cfg["outputSchema"].(map[string]any); ok {
		spec.OutputSchema = raw
	}

	inputs := map[string]any{}
	if rawInputs, ok := cfg["inputs"].([]any); ok {
		for _, entry := range rawInputs {
			name, ok := entry.(string)
			if !ok {
				continue
			}
			spec.Inputs = append(spec.Inputs, name)
			val, present := otherSources[name]
			if !present {
				return nil, fmt.Errorf("%w: live_compute input %q", ErrSourceMissing, name)
			}
			inputs[name] = val
		}
	}

	return deps.liveRunner.Run(ctx, spec, inputs)
}
