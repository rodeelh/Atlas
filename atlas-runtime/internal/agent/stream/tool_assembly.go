package stream

import "strings"

// toolAccum holds the partially-streamed state for one tool call.
type toolAccum struct {
	id               string
	typ              string
	name             string
	args             strings.Builder
	thoughtSignature string
}

// Assembler accumulates ToolDelta fragments keyed by index and assembles them
// into complete ToolCall values once the stream ends.
type Assembler struct {
	accums map[int]*toolAccum
}

// NewAssembler returns a ready-to-use Assembler.
func NewAssembler() *Assembler {
	return &Assembler{accums: make(map[int]*toolAccum)}
}

// Feed applies one ToolDelta to the accumulation state.
func (a *Assembler) Feed(d *ToolDelta) {
	acc := a.accums[d.Index]
	if acc == nil {
		acc = &toolAccum{}
		a.accums[d.Index] = acc
	}
	if d.ID != "" {
		acc.id = d.ID
	}
	if d.Type != "" {
		acc.typ = d.Type
	}
	if d.Name != "" {
		acc.name = d.Name
	}
	if d.ThoughtSignature != "" {
		acc.thoughtSignature = d.ThoughtSignature
	}
	acc.args.WriteString(d.ArgsDelta)
}

// ToolCall is one fully-assembled tool call ready for dispatch.
type ToolCall struct {
	Index            int
	ID               string
	Type             string
	Name             string
	Arguments        string
	ThoughtSignature string
}

// Assemble returns assembled tool calls in index order. Safe to call only
// after the stream has ended.
func (a *Assembler) Assemble() []ToolCall {
	if len(a.accums) == 0 {
		return nil
	}
	calls := make([]ToolCall, 0, len(a.accums))
	for i := 0; i < len(a.accums); i++ {
		acc, ok := a.accums[i]
		if !ok {
			break
		}
		tcType := "function"
		if acc.typ != "" {
			tcType = acc.typ
		}
		calls = append(calls, ToolCall{
			Index:            i,
			ID:               acc.id,
			Type:             tcType,
			Name:             acc.name,
			Arguments:        acc.args.String(),
			ThoughtSignature: acc.thoughtSignature,
		})
	}
	return calls
}
