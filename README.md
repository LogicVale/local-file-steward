# my_agent — 本地文件小管家智能体

一个用 Go 编写的命令行智能体（Agent），基于 [openai-go](https://github.com/openai/openai-go) 的 Responses API 对接 DeepSeek 大模型。内置本地文件管理工具，并支持通过 [MCP（Model Context Protocol）](https://modelcontextprotocol.io) 动态接入外部工具服务器。

## 功能特性

- **对话式交互**：命令行多轮对话，会话上下文全程保留
- **内置文件工具**：列目录、读文件、写文件、创建文件、重命名
- **工作区沙箱**：所有文件操作被限制在 `workspace/` 目录内，拒绝绝对路径和 `../` 越界
- **工具调用循环**：模型自主决定调用哪些工具，最多循环 `MaxLoop`（10）轮防止无限打转
- **MCP 扩展**：启动时读取 `mcp.json`，自动连接 MCP 服务器、发现工具，并与内置工具合并提供给大模型
- **内置演示 MCP 服务器**：以 `mcp-server` 子命令启动，提供时间查询、两数相加两个演示工具，开箱即用

## 项目结构

```
my_agent/
├── agent.go               # 程序入口：工具注册、对话主循环、MCP 接入
├── mcp.json               # MCP 服务器连接配置
├── go.mod / go.sum
├── core/
│   └── session.go         # 会话管理：维护发给 LLM 的完整消息历史
├── tools/
│   ├── tool.go            # 工具统一抽象 ToolDef（含 JSON 参数解析）
│   ├── safe_path.go       # 工作区沙箱：safeJoin 路径校验
│   ├── list_dir.go        # 列目录
│   ├── read_file.go       # 读文件
│   ├── write_file.go      # 覆盖写文件
│   ├── create_file.go     # 创建新文件（已存在则报错）
│   └── rename_file.go     # 重命名
├── mcpclient/
│   └── mcpclient.go       # MCP 客户端：连接服务器、发现工具、包装为 ToolDef
├── mcpserver/
│   └── mcpserver.go       # 内置演示 MCP 服务器
└── workspace/             # 智能体的工作区（所有文件操作的根目录）
```

## 快速开始

### 1. 配置 API Key

打开 [agent.go](agent.go)，把 `option.WithAPIKey("sk-...")` 里的内容替换为你自己的 DeepSeek API Key（建议改用环境变量读取，避免密钥入库）。

### 2. 安装依赖

```bash
go mod tidy
```

### 3. 运行

```bash
go run .
```

启动后会打印工作区目录和已注册的工具列表（内置 + MCP），然后进入对话：

```
===== 本地文件小管家智能体 =====
工作区目录: C:\...\my_agent\workspace
已注册工具 7 个（内置 5 个 + MCP 2 个）
  [MCP] mcp__demo__current_time - [MCP 服务器 demo] 获取当前系统时间，可指定 IANA 时区名
  [MCP] mcp__demo__sum - [MCP 服务器 demo] 计算两个数字的和
输入你想做的事（例如：查看当前目录有哪些文件），输入 quit 退出。

你> 现在几点了？
你> 帮我算一下 12 加 88
你> 查看当前目录有哪些文件
你> 帮我创建 todo.txt，内容写"买牛奶"
```

输入 `quit` 或 `exit` 退出。

### 4. 编译发布

```bash
go build -o my_agent.exe .
```

> 提示：MCP 的 `"self"` 服务器依赖 `os.Executable()` 定位自身，`go run .` 与编译后的 exe 均可正常工作。

## 内置工具

| 工具名 | 说明 | 参数 |
|---|---|---|
| `list_dir` | 列出工作区目录内容 | `path`（相对路径，默认根目录） |
| `read_file` | 读取文件内容 | `path`（必填） |
| `create_file` | 创建新文件，已存在则报错 | `path`、`content`（必填） |
| `write_file` | 覆盖写入文件，不存在则创建 | `path`、`content`（必填） |
| `rename_file` | 重命名文件/目录 | `old_path`、`new_path`（必填） |

## 工作区与安全机制

智能体对文件的所有操作都经过 [tools/safe_path.go](tools/safe_path.go) 的 `safeJoin` 校验，实现两道防线：

1. **拒绝绝对路径** —— 防止模型直接访问 `C:\Windows\...` 等系统目录；
2. **越界检测** —— 路径拼接后做 `filepath.Clean` 再比对前缀，`..\..\x` 这类相对路径跳跃会被拒绝。

最终所有读写都限制在 `workspace/` 目录内，`workspace/` 不存在时会自动创建。

## MCP 扩展

### 工作原理

本项目作为 **MCP 客户端**，通过 stdio 方式把 MCP 服务器作为子进程启动，完成 initialize 握手后调用 `tools/list` 发现工具，再把每个工具包装成与内置工具一致的 `ToolDef` 合并进工具表。大模型调用 MCP 工具时，客户端通过 `tools/call` 把参数转发给对应服务器执行，结果回传给模型。

```
┌──────────┐  mcp.json  ┌─────────────┐  stdio (JSON-RPC)  ┌──────────────┐
│ my_agent │ 读取配置 → │ mcpclient   │ ────启动/握手────→ │ MCP 服务器    │
│ 智能体    │            │ Manager     │ ←── tools/list ─── │ (子进程)      │
└──────────┘            └─────────────┘ ──── tools/call ─→ └──────────────┘
```

### 配置文件 mcp.json

```json
{
  "mcpServers": {
    "服务器名": {
      "command": "要启动的可执行文件",
      "args": ["启动参数"],
      "env": ["KEY=VALUE"]
    }
  }
}
```

- `command` 为 `"self"` 时表示启动本程序自身（配合 `mcp-server` 子命令），用于演示；
- `command` 也可以是任何标准 MCP 服务器，例如 `npx`、`node`、`python` 等；
- 配置文件不存在时智能体照常运行，只是没有 MCP 工具。

### 内置演示服务器

本仓库内置了一个演示 MCP 服务器（[mcpserver/mcpserver.go](mcpserver/mcpserver.go)），提供：

| 工具名 | 说明 | 参数 |
|---|---|---|
| `current_time` | 查询当前系统时间 | `tz_name`（可选，IANA 时区名） |
| `sum` | 两数相加 | `x`、`y`（必填） |

它通过同一个二进制以子命令方式启动，也可以单独运行验证：

```bash
go run . mcp-server
```

> MCP 服务器通过 stdout 收发 JSON-RPC 协议消息，日志一律走 stderr，因此不要在服务器代码里向 stdout 打印普通日志。

### 接入外部 MCP 服务器示例

在 `mcp.json` 中追加条目即可（重启程序生效）：

```json
{
  "mcpServers": {
    "demo": {
      "command": "self",
      "args": ["mcp-server"]
    },
    "everything": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-everything"]
    }
  }
}
```

### 工具命名规则

为避免与内置工具重名，MCP 工具统一改名为 `mcp__服务器名__原工具名`，例如 `mcp__demo__current_time`；工具描述前会加上 `[MCP 服务器 xxx]` 前缀，帮助模型区分工具来源。

## 一轮对话的工作流程

```
用户输入
   │
   ▼
runOnce() 内部循环（最多 MaxLoop=10 轮）：
   ① 把全部会话历史 + 工具清单（内置 + MCP）发给 DeepSeek
   ② 模型返回 function_call（工具名 + 参数 JSON）
   ③ 本地查表执行工具（内置工具直接执行 / MCP 工具转发给服务器）
   ④ 工具结果以 function_call_output 存回会话
   ⑤ 再次请求模型……直到模型不再调用工具，给出文字回答
   │
   ▼
打印 [助手] 最终回答，回到等待输入
```

也就是说：调用一次 `runOnce` 可能包含多次模型请求——模型只负责“决策”（输出想调用的工具和参数），本地代码负责“执行”（校验、落盘、转发 MCP），沙箱负责兜底安全。

## 常见问题

- **提示 401 / 鉴权失败**：检查 [agent.go](agent.go) 中的 API Key 是否已替换为你自己的。
- **go get 拉取依赖失败（proxyconnect refused）**：本地代理未启动导致，清空 `HTTP_PROXY`/`HTTPS_PROXY` 环境变量后重试，或改用可达的 GOPROXY。
- **[MCP] 连接服务器 xxx 失败**：核对 `mcp.json` 里的 `command` 是否存在、参数是否正确；某个服务器连接失败不影响其他工具使用。
- **模型不停调用工具**：`MaxLoop` 常量限制了单轮对话最多 10 次工具循环，超限会自动结束并提示。
