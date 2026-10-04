package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/dynamictool/toolsearch"
	"github.com/cloudwego/eino/components/tool"

	orchapp "orkome/internal/orchestration/app"
)

const ToolSearchThreshold = 24

type toolPack struct {
	static []tool.BaseTool

	dynamic []tool.BaseTool

	searchMW adk.ChatModelAgentMiddleware
}

func (e *EinoEngine) buildToolPack(ctx context.Context, task orchapp.Task, agent orchapp.Agent,
	core []tool.BaseTool, memberTools []tool.BaseTool, bridge *mcpBridge) toolPack {
	pack := toolPack{static: core}
	pack.static = append(pack.static, memberTools...)
	mcpTools := []tool.BaseTool(nil)
	if bridge != nil {
		mcpTools = bridge.tools
	}
	if len(pack.static)+len(mcpTools) <= ToolSearchThreshold {
		pack.static = append(pack.static, mcpTools...)
		return pack
	}

	mw, err := toolsearch.New(ctx, &toolsearch.Config{DynamicTools: mcpTools})
	if err != nil {
		pack.static = append(pack.static, mcpTools...)
		return pack
	}
	pack.dynamic = mcpTools
	pack.searchMW = mw
	return pack
}

func unknownToolsHandler(known func() []string) func(ctx context.Context, name, input string) (string, error) {
	return func(_ context.Context, name, _ string) (string, error) {
		return fmt.Sprintf(
			"Tool %q does not exist. Available tools: %s. Pick from the list; for external capabilities check the MCP server configuration.",
			name, strings.Join(known(), "、")), nil
	}
}

func toolNamesOf(ctx context.Context, tools []tool.BaseTool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if info, err := t.Info(ctx); err == nil {
			names = append(names, info.Name)
		}
	}
	return names
}
