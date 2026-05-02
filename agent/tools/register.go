package tools

import (
	"encoding/json"
	"github.com/xumi30/fullmodel/agent/brain"
	"sync"
)

// registry implements the Toolregistry interface
type registry struct {
	tools      map[string]Tool
	mu         sync.RWMutex
	topictools map[string][]Tool
}

// Newregistry creates a new tool registry
func newregistry() *registry {
	return &registry{
		tools:      make(map[string]Tool),
		topictools: make(map[string][]Tool),
	}
}

// Register registers a tool with the registry
func (r *registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool

	// Build topic -> tools index for intent routing / lexicons.
	// Topic is provided by Tool.SimpleInfo(): {"topic": "...", "simpledescription": "..."}.
	if si := tool.SimpleInfo(); si != nil {
		if topic := si["topic"]; topic != "" {
			r.topictools[topic] = append(r.topictools[topic], tool)
		}
	}
}

// Get returns a tool by name
func (r *registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

// List returns all registered tools
func (r *registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var tools []Tool
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	return tools
}

// ListByTopic returns all tools registered under a topic.
func (r *registry) ListByTopic(topic string) []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	src := r.topictools[topic]
	if len(src) == 0 {
		return nil
	}
	// Return a copy to avoid external mutation.
	dst := make([]Tool, len(src))
	copy(dst, src)
	return dst
}

func (r *registry) ConvertToolsByTopic(topic string) []brain.Tool {
	toolsList := make([]brain.Tool, 0)
	for _, tool := range r.ListByTopic(topic) {
		chatTool := brain.Tool{
			Type: "function",
			Function: brain.FunctionDefinition{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		}
		toolsList = append(toolsList, chatTool)
	}

	return toolsList
}

func (r *registry) ConvertTools() []brain.Tool {
	toolsList := make([]brain.Tool, 0)
	for _, tool := range r.List() {
		chatTool := brain.Tool{
			Type: "function",
			Function: brain.FunctionDefinition{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		}
		toolsList = append(toolsList, chatTool)
	}

	return toolsList
}

var (
	registryInstance *registry
	registryOnce     sync.Once
)

// Getregistry 获取全局单例registry对象
func Getregistry() *registry {
	registryOnce.Do(func() {
		registryInstance = newregistry()
	})
	return registryInstance
}

// 遍历注册器，逐个json化
func (r *registry) ConvertToolsToJSON() ([]byte, error) {
	toolsList := make([]map[string]interface{}, 0)
	for _, tool := range r.List() {
		toolInfo := map[string]interface{}{
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters":  tool.Parameters(),
			"results":     tool.Results(),
			"simple_info": tool.SimpleInfo(),
		}
		toolsList = append(toolsList, toolInfo)
	}
	//logging.Info("Converted tools to JSON format %s", toolsList)
	return json.MarshalIndent(toolsList, "", "  ")
}

func (r *registry) GetToolsSimpleInfo() ([]byte, error) {
	toolsList := make([]map[string]interface{}, 0)
	for _, tool := range r.List() {
		toolInfo := map[string]interface{}{
			"name":        tool.Name(),
			"description": tool.SimpleInfo(),
		}
		toolsList = append(toolsList, toolInfo)
	}
	//logging.Info("Converted tools to JSON format %s", toolsList)
	return json.MarshalIndent(toolsList, "", "  ")
}
