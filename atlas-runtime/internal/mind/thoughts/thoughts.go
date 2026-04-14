// Package thoughts is the data layer for Atlas mind thoughts — persistent
// open loops the agent tends during naps and the nightly dream cycle.
//
// Design principles (see project_mind_thoughts.md memory for the full spec):
//
//   - Thoughts are fleeting. A discarded thought is genuinely gone. Atlas does
//     not remember having had it. Telemetry preserves the lifecycle for designers
//     but the system itself has no access to its own graveyard.
//
//   - Structure is the contract, prose is the content. Thought bodies are
//     freeform sentences in Atlas's voice. Metadata around them is structured
//     so code can rank, decay, and surface them.
//
//   - Safety is a math property, not a runtime check. Score =
//     (confidence × value × safety_multiplier[class]) / 100. The multiplier
//     for risky classes is small enough that the 95 auto-execute threshold
//     is structurally unreachable. The model proposes confidence and value;
//     only code computes the score.
//
//   - The active set is small on purpose. Decay equilibrium keeps it at ~1–3
//     thoughts in steady state. There is no queue manager — thoughts that
//     don't earn reinforcement die naturally.
//
// This package is pure data + pure functions. No I/O, no agent calls, no
// network. It can be tested exhaustively in isolation.
package thoughts

import (
	"fmt"
	"time"
)

// ActionClass mirrors the skills package ActionClass enum. Duplicated here
// instead of imported to keep this package free of dependencies on the skills
// registry — important because the registry imports many other things and
// this package needs to remain a leaf.
type ActionClass string

const (
	ClassRead               ActionClass = "read"
	ClassLocalWrite         ActionClass = "local_write"
	ClassDestructiveLocal   ActionClass = "destructive_local"
	ClassExternalSideEffect ActionClass = "external_side_effect"
	ClassSendPublishDelete  ActionClass = "send_publish_delete"
)

// Valid returns true if c is a recognized ActionClass.
func (c ActionClass) Valid() bool {
	switch c {
	case ClassRead, ClassLocalWrite, ClassDestructiveLocal,
		ClassExternalSideEffect, ClassSendPublishDelete:
		return true
	}
	return false
}

// ProposedAction optionally points a thought at a concrete skill call the
// agent believes Atlas could make. Used by the dispatcher in phase 4 to
// auto-execute high-scoring read-class thoughts or route others to approvals.
// Nil means the thought is pure reflection with no proposed action.
type ProposedAction struct {
	SkillID string         `json:"skill"` // e.g. "openclaw-latest-build-changes.check-latest-build-changes"
	Args    map[string]any `json:"args,omitempty"`
}

// Thought is one open loop in Atlas's mind. Lives inside the ## THOUGHTS
// section of MIND.md as a markdown bullet with inline metadata. The body is
// freeform prose; everything else is the structured contract.
type Thought struct {
	ID          string          `json:"id"`         // "T-01", "T-02", …
	Body        string          `json:"body"`       // freeform prose, Atlas's voice
	Confidence  int             `json:"confidence"` // 0–100, model-proposed
	Value       int             `json:"value"`      // 0–100, model-proposed
	Class       ActionClass     `json:"class"`      // model-proposed
	Score       int             `json:"score"`      // COMPUTED BY CODE, never model
	Created     time.Time       `json:"created"`
	Reinforced  time.Time       `json:"reinforced"`
	SurfacedN   int             `json:"surfaced"`     // how many times raised in chat
	SurfacedMax int             `json:"surfaced_max"` // session cap, typically 2
	Source      string          `json:"source"`       // "conv-7f3a:nap-3" or "nap-spontaneous"
	Provenance  string          `json:"provenance"`   // one-line why-this-came-up trace
	Action      *ProposedAction `json:"action,omitempty"`
}

// Clone returns a deep copy of t suitable for independent mutation.
func (t Thought) Clone() Thought {
	out := t
	if t.Action != nil {
		act := *t.Action
		if t.Action.Args != nil {
			act.Args = make(map[string]any, len(t.Action.Args))
			for k, v := range t.Action.Args {
				act.Args[k] = v
			}
		}
		out.Action = &act
	}
	return out
}

// Validate returns an error if the thought is missing required fields or has
// out-of-range values. Used both when parsing persisted state and when
// accepting model-proposed ops.
func (t Thought) Validate() error {
	if t.ID == "" {
		return fmt.Errorf("thought: empty id")
	}
	if t.Body == "" {
		return fmt.Errorf("thought %s: empty body", t.ID)
	}
	if t.Confidence < 0 || t.Confidence > 100 {
		return fmt.Errorf("thought %s: confidence out of range: %d", t.ID, t.Confidence)
	}
	if t.Value < 0 || t.Value > 100 {
		return fmt.Errorf("thought %s: value out of range: %d", t.ID, t.Value)
	}
	if !t.Class.Valid() {
		return fmt.Errorf("thought %s: invalid class: %q", t.ID, t.Class)
	}
	if t.Score < 0 || t.Score > 100 {
		return fmt.Errorf("thought %s: score out of range: %d", t.ID, t.Score)
	}
	if t.SurfacedN < 0 {
		return fmt.Errorf("thought %s: negative surfaced count", t.ID)
	}
	if t.Created.IsZero() {
		return fmt.Errorf("thought %s: zero created timestamp", t.ID)
	}
	return nil
}

// OpKind identifies the type of edit operation a nap or dream cycle proposes.
type OpKind string

const (
	OpAdd       OpKind = "add"
	OpUpdate    OpKind = "update"
	OpReinforce OpKind = "reinforce"
	OpDiscard   OpKind = "discard"
	OpMerge     OpKind = "merge"
)

// Op is one edit to the THOUGHTS section. Produced by the nap cycle in JSON
// form, applied by Apply. Code owns the score computation — the model never
// writes the Score field, only Confidence, Value, and Class.
type Op struct {
	Kind OpKind `json:"op"`

	// For OpAdd — required fields to mint a new thought.
	Body       string          `json:"body,omitempty"`
	Confidence int             `json:"confidence,omitempty"`
	Value      int             `json:"value,omitempty"`
	Class      ActionClass     `json:"class,omitempty"`
	Source     string          `json:"source,omitempty"`
	Provenance string          `json:"provenance,omitempty"`
	Action     *ProposedAction `json:"action,omitempty"`

	// For OpUpdate, OpReinforce, OpDiscard — targets an existing thought.
	ID string `json:"id,omitempty"`

	// For OpMerge — combines two or more thoughts into one.
	IDs      []string `json:"ids,omitempty"`
	IntoBody string   `json:"into_body,omitempty"`
}

// Envelope is the JSON object a nap returns. A rationale plus a list of ops.
type Envelope struct {
	Rationale string `json:"rationale"`
	Ops       []Op   `json:"ops"`
}
