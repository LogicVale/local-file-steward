// Package mcpserver 内置的演示用 MCP 服务器。
// 通过 `my_agent.exe mcp-server` 启动，以 stdio 方式与 MCP 客户端通信，
// 提供 current_time（查询时间）和 sum（两数相加）两个演示工具。
package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Run 启动内置 MCP 服务器（阻塞运行，直到客户端断开）
func Run() error {
	s := server.NewMCPServer(
		"my_agent_demo",
		"0.1.0",
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)

	// 工具 1：查询当前时间
	timeTool := mcp.NewTool("current_time",
		mcp.WithDescription("获取当前系统时间，可指定 IANA 时区名"),
		mcp.WithString("tz_name",
			mcp.Description("可选，IANA 时区名，例如 Asia/Shanghai；不传则使用本地时区"),
		),
	)
	s.AddTool(timeTool, handleCurrentTime)

	// 工具 2：两数相加
	sumTool := mcp.NewTool("sum",
		mcp.WithDescription("计算两个数字的和"),
		mcp.WithNumber("x", mcp.Required(), mcp.Description("第一个数字")),
		mcp.WithNumber("y", mcp.Required(), mcp.Description("第二个数字")),
	)
	s.AddTool(sumTool, handleSum)

	return server.ServeStdio(s)
}

func handleCurrentTime(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	loc := time.Local
	if name := req.GetString("tz_name", ""); name != "" {
		l, err := time.LoadLocation(name)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("无效时区: %s", name)), nil
		}
		loc = l
	}
	return mcp.NewToolResultText(time.Now().In(loc).Format("2006-01-02 15:04:05 (MST)")), nil
}

func handleSum(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	x, err := req.RequireFloat("x")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	y, err := req.RequireFloat("y")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("%g + %g = %g", x, y, x+y)), nil
}
