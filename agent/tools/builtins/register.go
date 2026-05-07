package builtins

import (
	"sync"

	"github.com/xumi30/fullmodel/agent/tools"
	searchFunctions "github.com/xumi30/fullmodel/agent/tools/searchFuctions"
	"github.com/xumi30/fullmodel/agent/tools/timeFunctions"
)

type Registry interface {
	Register(tool tools.Tool)
}

var once sync.Once

// Register installs the safe default tools. It intentionally excludes local
// command execution; applications can opt into that separately.
func Register(registry Registry) {
	if registry == nil {
		return
	}
	once.Do(func() {
		registry.Register(timeFunctions.NewCurrentTimeTool())
		registry.Register(timeFunctions.NewTimeTool())
		registry.Register(timeFunctions.NewCalculateTimeTool())
		registry.Register(searchFunctions.NewGeocodingTool())
		registry.Register(searchFunctions.NewWeatherTool())
		registry.Register(searchFunctions.NewWikipediaSearchTool())
		registry.Register(searchFunctions.NewBaiduSearchTool())
		registry.Register(searchFunctions.NewMarketTool())
	})
}
