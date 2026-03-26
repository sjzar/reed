package tool

import (
	"fmt"
	"sort"
	"sync"

	"github.com/sjzar/reed/internal/model"
)

// Registry is a thread-safe tool registry.
type Registry struct {
	mu     sync.RWMutex
	tools  map[string]Tool
	groups map[string]ToolGroup
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		tools:  make(map[string]Tool),
		groups: make(map[string]ToolGroup),
	}
}

// Register adds one or more tools. Returns error if a tool with the same name already exists.
// If a tool implements GroupedTool, its group is used; otherwise GroupOptional.
func (r *Registry) Register(tools ...Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range tools {
		name := t.Def().Name
		if _, exists := r.tools[name]; exists {
			return fmt.Errorf("tool already registered: %s", name)
		}
		r.tools[name] = t
		if gt, ok := t.(GroupedTool); ok {
			r.groups[name] = gt.Group()
		} else {
			r.groups[name] = GroupOptional
		}
	}
	return nil
}

// RegisterWithGroup adds a tool with an explicit group override.
func (r *Registry) RegisterWithGroup(t Tool, group ToolGroup) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Def().Name
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool already registered: %s", name)
	}
	r.tools[name] = t
	r.groups[name] = group
	return nil
}

// CoreToolIDs returns the names of all tools in GroupCore, sorted alphabetically.
func (r *Registry) CoreToolIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var ids []string
	for name, g := range r.groups {
		if g == GroupCore {
			ids = append(ids, name)
		}
	}
	sort.Strings(ids)
	return ids
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// ListTools returns ToolDefs for the given IDs.
// Empty or nil ids returns all tools. Unknown IDs cause an error.
func (r *Registry) ListTools(ids []string) ([]model.ToolDef, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(ids) == 0 {
		names := make([]string, 0, len(r.tools))
		for name := range r.tools {
			names = append(names, name)
		}
		sort.Strings(names)
		defs := make([]model.ToolDef, 0, len(names))
		for _, name := range names {
			defs = append(defs, r.tools[name].Def())
		}
		return defs, nil
	}

	defs := make([]model.ToolDef, 0, len(ids))
	for _, id := range ids {
		t, ok := r.tools[id]
		if !ok {
			return nil, fmt.Errorf("unknown tool: %s", id)
		}
		defs = append(defs, t.Def())
	}
	return defs, nil
}

// ListToolsLenient is like ListTools but silently skips unknown IDs instead of
// returning an error. Used for skill-derived tool lists where tools may
// reference provider-specific names not in the registry.
func (r *Registry) ListToolsLenient(ids []string) []model.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]model.ToolDef, 0, len(ids))
	for _, id := range ids {
		if t, ok := r.tools[id]; ok {
			defs = append(defs, t.Def())
		}
	}
	return defs
}

// All returns all registered tools, sorted alphabetically by name.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]Tool, 0, len(names))
	for _, name := range names {
		result = append(result, r.tools[name])
	}
	return result
}
