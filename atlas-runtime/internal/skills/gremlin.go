package skills

import (
	"context"
	"encoding/json"
	"fmt"
)

func (r *Registry) registerGremlin() {
	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "gremlin.create",
			Description: "Create a new automation (Gremlin) in GREMLINS.md.",
			Properties: map[string]ToolParam{
				"name":        {Description: "A short display name for the automation", Type: "string"},
				"prompt":      {Description: "The prompt Atlas will run on schedule", Type: "string"},
				"schedule":    {Description: "Human-readable schedule, e.g. 'every day at 9am' or 'every Monday'", Type: "string"},
				"emoji":       {Description: "An emoji representing the automation (default ⚡)", Type: "string"},
				"description": {Description: "Optional description of what this automation does", Type: "string"},
				"enabled":     {Description: "Whether to enable immediately (default true)", Type: "boolean"},
			},
			Required: []string{"name", "prompt", "schedule"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassLocalWrite,
		Fn:          r.gremlinCreate,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "gremlin.update",
			Description: "Update an existing automation (Gremlin) by ID.",
			Properties: map[string]ToolParam{
				"id":          {Description: "The gremlin ID (slugified name)", Type: "string"},
				"name":        {Description: "New display name", Type: "string"},
				"prompt":      {Description: "New prompt text", Type: "string"},
				"schedule":    {Description: "New schedule string", Type: "string"},
				"emoji":       {Description: "New emoji", Type: "string"},
				"enabled":     {Description: "Enable or disable the automation", Type: "boolean"},
				"description": {Description: "New description", Type: "string"},
			},
			Required: []string{"id"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassLocalWrite,
		Fn:          r.gremlinUpdate,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name: "gremlin.delete",
			Description: "Delete an automation (Gremlin) by ID. " +
				"Use gremlin.list or the Automations screen to find the ID.",
			Properties: map[string]ToolParam{
				"id": {Description: "The gremlin ID to delete", Type: "string"},
			},
			Required: []string{"id"},
		},
		PermLevel:   "execute",
		ActionClass: ActionClassDestructiveLocal,
		Fn:          r.gremlinDelete,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "gremlin.list",
			Description: "List all automations (Gremlins) and their current state.",
			Properties:  map[string]ToolParam{},
			Required:    []string{},
		},
		PermLevel: "read",
		Fn:        r.gremlinList,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "gremlin.get",
			Description: "Get the full details of a single automation by ID.",
			Properties: map[string]ToolParam{
				"id": {Description: "The gremlin ID (slugified name)", Type: "string"},
			},
			Required: []string{"id"},
		},
		PermLevel: "read",
		Fn:        r.gremlinGet,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "gremlin.enable",
			Description: "Enable a disabled automation by ID.",
			Properties: map[string]ToolParam{
				"id": {Description: "The gremlin ID to enable", Type: "string"},
			},
			Required: []string{"id"},
		},
		PermLevel: "execute",
		Fn:        r.gremlinEnable,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "gremlin.disable",
			Description: "Disable a running automation by ID.",
			Properties: map[string]ToolParam{
				"id": {Description: "The gremlin ID to disable", Type: "string"},
			},
			Required: []string{"id"},
		},
		PermLevel: "execute",
		Fn:        r.gremlinDisable,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "gremlin.run_now",
			Description: "Immediately trigger a scheduled automation by ID.",
			Properties: map[string]ToolParam{
				"id": {Description: "The gremlin ID to run", Type: "string"},
			},
			Required: []string{"id"},
		},
		PermLevel: "execute",
		Fn:        r.gremlinRunNow,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "gremlin.run_history",
			Description: "Show recent run history for an automation.",
			Properties: map[string]ToolParam{
				"id":    {Description: "Gremlin ID (required)", Type: "string"},
				"limit": {Description: "Max runs to return (default 10)", Type: "integer"},
			},
			Required: []string{"id"},
		},
		PermLevel: "read",
		Fn:        r.gremlinRunHistory,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "gremlin.next_run",
			Description: "Calculate and return the next scheduled run time for an automation.",
			Properties: map[string]ToolParam{
				"id": {Description: "The gremlin ID", Type: "string"},
			},
			Required: []string{"id"},
		},
		PermLevel: "read",
		Fn:        r.gremlinNextRun,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "gremlin.duplicate",
			Description: "Duplicate an existing automation under a new name.",
			Properties: map[string]ToolParam{
				"id":      {Description: "Source gremlin ID to duplicate", Type: "string"},
				"newName": {Description: "Name for the duplicate", Type: "string"},
			},
			Required: []string{"id", "newName"},
		},
		PermLevel: "execute",
		Fn:        r.gremlinDuplicate,
	})

	r.register(SkillEntry{
		Def: ToolDef{
			Name:        "gremlin.validate_schedule",
			Description: "Validate a schedule string and return the interpreted schedule or an error.",
			Properties: map[string]ToolParam{
				"schedule": {Description: "Schedule string to validate, e.g. 'every day at 9am' or 'cron 0 9 * * *'", Type: "string"},
			},
			Required: []string{"schedule"},
		},
		PermLevel: "read",
		Fn:        r.gremlinValidateSchedule,
	})
}

func (r *Registry) gremlinAlias(ctx context.Context, actionID string, args json.RawMessage) (string, error) {
	res, err := r.Execute(ctx, actionID, args)
	if err != nil {
		return "", err
	}
	if !res.Success {
		return "", fmt.Errorf("%s", res.Summary)
	}
	return res.Summary, nil
}

func (r *Registry) gremlinCreate(ctx context.Context, args json.RawMessage) (string, error) {
	return r.gremlinAlias(ctx, "automation.create", args)
}

func (r *Registry) gremlinUpdate(ctx context.Context, args json.RawMessage) (string, error) {
	return r.gremlinAlias(ctx, "automation.update", args)
}

func (r *Registry) gremlinDelete(ctx context.Context, args json.RawMessage) (string, error) {
	return r.gremlinAlias(ctx, "automation.delete", args)
}

func (r *Registry) gremlinList(ctx context.Context, args json.RawMessage) (string, error) {
	return r.gremlinAlias(ctx, "automation.list", args)
}

func (r *Registry) gremlinGet(ctx context.Context, args json.RawMessage) (string, error) {
	return r.gremlinAlias(ctx, "automation.get", args)
}

func (r *Registry) gremlinEnable(ctx context.Context, args json.RawMessage) (string, error) {
	return r.gremlinAlias(ctx, "automation.enable", args)
}

func (r *Registry) gremlinDisable(ctx context.Context, args json.RawMessage) (string, error) {
	return r.gremlinAlias(ctx, "automation.disable", args)
}

func (r *Registry) gremlinRunNow(ctx context.Context, args json.RawMessage) (string, error) {
	return r.gremlinAlias(ctx, "automation.run", args)
}

func (r *Registry) gremlinRunHistory(ctx context.Context, args json.RawMessage) (string, error) {
	return r.gremlinAlias(ctx, "automation.run_history", args)
}

func (r *Registry) gremlinNextRun(ctx context.Context, args json.RawMessage) (string, error) {
	return r.gremlinAlias(ctx, "automation.next_run", args)
}

func (r *Registry) gremlinDuplicate(ctx context.Context, args json.RawMessage) (string, error) {
	return r.gremlinAlias(ctx, "automation.duplicate", args)
}

func (r *Registry) gremlinValidateSchedule(ctx context.Context, args json.RawMessage) (string, error) {
	return r.gremlinAlias(ctx, "automation.validate_schedule", args)
}
