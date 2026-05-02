package runtime

import (
	"fmt"
	"sort"

	"fullmodel/agent/brain"
	"fullmodel/processmessage"
)

// Capability describes one message capability exposed by the runtime.
type Capability struct {
	Kind        processmessage.Kind `json:"kind"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Streaming   bool                `json:"streaming"`
}

// Registry owns the mapping from message kinds to brains.
type Registry struct {
	brains       map[processmessage.Kind]brain.Brain
	capabilities map[processmessage.Kind]Capability
}

func NewRegistry() *Registry {
	return &Registry{
		brains:       make(map[processmessage.Kind]brain.Brain),
		capabilities: make(map[processmessage.Kind]Capability),
	}
}

func (r *Registry) Register(kind processmessage.Kind, b brain.Brain, capability Capability) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	if kind == "" {
		return fmt.Errorf("message kind is empty")
	}
	if b == nil {
		return fmt.Errorf("brain for %q is nil", kind)
	}
	if capability.Kind == "" {
		capability.Kind = kind
	}
	r.brains[kind] = b
	r.capabilities[kind] = capability
	return nil
}

func (r *Registry) SelectBrain(kind processmessage.Kind) (brain.Brain, bool) {
	if r == nil {
		return nil, false
	}
	b, ok := r.brains[kind]
	return b, ok
}

func (r *Registry) Capability(kind processmessage.Kind) (Capability, bool) {
	if r == nil {
		return Capability{}, false
	}
	c, ok := r.capabilities[kind]
	return c, ok
}

func (r *Registry) Capabilities() []Capability {
	if r == nil {
		return nil
	}
	out := make([]Capability, 0, len(r.capabilities))
	for _, capability := range r.capabilities {
		out = append(out, capability)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Kind < out[j].Kind
	})
	return out
}
