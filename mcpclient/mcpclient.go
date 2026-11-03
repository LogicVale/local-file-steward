// Package mcpclient 把 MCP 服务器的工具接入智能体。
// 它读取 mcp.json 配置，通过 stdio 连接各 MCP 服务器，
// 发现其工具并包装成与内置工具一致的 tools.ToolDef，
// 让大模型可以像调用内置工具一样调用 MCP 工具。
package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"my_agent/tools"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openai/openai-go/v3"
)

// MCPServerConfig 对应 mcp.json 里的一个服务器条目
type MCPServerConfig struct {
	Command string   `json:"command"` // 要启动的可执行文件；"self" 表示本程序自身
	Args    []string `json:"args"`    // 启动参数
	Env     []string `json:"env"`     // 额外环境变量，格式 KEY=VALUE
}

// mcpConfigFile mcp.json 的整体结构
type mcpConfigFile struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// Manager 管理所有已连接的 MCP 服务器
type Manager struct {
	clients map[string]*client.Client
}

func NewManager() *Manager {
	return &Manager{clients: make(map[string]*client.Client)}
}

// LoadConfig 读取 mcp.json。文件不存在时返回空配置（不算错误）。
func LoadConfig(path string) (map[string]MCPServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cfg mcpConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	return cfg.MCPServers, nil
}

// ConnectAll 连接配置里的所有 MCP 服务器，单个服务器失败只告警不中断
func (m *Manager) ConnectAll(ctx context.Context, configs map[string]MCPServerConfig) error {
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if err := m.connect(ctx, name, configs[name]); err != nil {
			fmt.Printf("[MCP] 连接服务器 %s 失败: %v\n", name, err)
		}
	}
	return nil
}

// connect 启动某个 MCP 服务器子进程并完成初始化握手
func (m *Manager) connect(ctx context.Context, name string, cfg MCPServerConfig) error {
	cmd := cfg.Command
	if cmd == "self" {
		// "self" 表示用本程序自身作为 MCP 服务器（配合 mcp-server 子命令）
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cmd = exe
	}
	if _, err := exec.LookPath(cmd); err != nil {
		return fmt.Errorf("找不到命令 %q", cmd)
	}

	c, err := client.NewStdioMCPClient(cmd, cfg.Env, cfg.Args...)
	if err != nil {
		return fmt.Errorf("启动子进程失败: %w", err)
	}

	connCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := c.Start(connCtx); err != nil {
		c.Close()
		return fmt.Errorf("建立连接失败: %w", err)
	}

	// MCP 初始化握手
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "my_agent", Version: "0.1.0"}
	if _, err := c.Initialize(connCtx, initReq); err != nil {
		c.Close()
		return fmt.Errorf("初始化握手失败: %w", err)
	}

	m.clients[name] = c
	fmt.Printf("[MCP] 已连接服务器 %s (%s)\n", name, cmd)
	return nil
}

// Tools 把所有 MCP 服务器暴露的工具包装成 ToolDef，
// 工具名格式为 mcp__服务器名__工具名，避免与内置工具重名。
func (m *Manager) Tools() map[string]*tools.ToolDef {
	result := make(map[string]*tools.ToolDef)
	for serverName, c := range m.clients {
		toolsRes, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
		if err != nil {
			fmt.Printf("[MCP] 获取服务器 %s 的工具列表失败: %v\n", serverName, err)
			continue
		}
		for _, t := range toolsRes.Tools {
			td := wrapTool(c, serverName, t)
			result[td.Name] = td
		}
	}
	return result
}

// wrapTool 把一个 MCP 工具包装成智能体统一使用的 ToolDef
func wrapTool(c *client.Client, serverName string, t mcp.Tool) *tools.ToolDef {
	return &tools.ToolDef{
		Name:        fmt.Sprintf("mcp__%s__%s", serverName, t.Name),
		Description: fmt.Sprintf("[MCP 服务器 %s] %s", serverName, t.Description),
		Params:      convertSchema(t),
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			return callTool(ctx, c, t.Name, args)
		},
	}
}

// convertSchema 把 MCP 的 InputSchema 转成 OpenAI 的 FunctionParameters
func convertSchema(t mcp.Tool) openai.FunctionParameters {
	fallback := openai.FunctionParameters{
		"type":       "object",
		"properties": map[string]any{},
	}
	if t.InputSchema.Type == "" {
		return fallback
	}
	raw, err := json.Marshal(t.InputSchema)
	if err != nil {
		return fallback
	}
	var params openai.FunctionParameters
	if err := json.Unmarshal(raw, &params); err != nil {
		return fallback
	}
	return params
}

// callTool 通过 MCP 协议调用远端工具，并把返回的文本内容拼成字符串
func callTool(ctx context.Context, c *client.Client, name string, args map[string]any) (string, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	res, err := c.CallTool(ctx, req)
	if err != nil {
		return "", err
	}
	if res.IsError {
		return "", fmt.Errorf("MCP 工具执行出错")
	}
	var sb strings.Builder
	for _, item := range res.Content {
		switch v := item.(type) {
		case mcp.TextContent:
			sb.WriteString(v.Text)
		default:
			fmt.Fprintf(&sb, "%v", v)
		}
	}
	return sb.String(), nil
}

// Close 关闭所有 MCP 连接
func (m *Manager) Close() {
	for _, c := range m.clients {
		c.Close()
	}
}
