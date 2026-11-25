package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"my_agent/core"
	"my_agent/mcpclient"
	"my_agent/mcpserver"
	"my_agent/memory"
	"my_agent/skills"
	"my_agent/tools"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

const MaxLoop = 10

// modelName 从环境变量 DEEPSEEK_MODEL 读取，默认 deepseek-v4-pro。
// 在 main 中初始化，供 runOnce / generateSessionSummary 共享。
var modelName = "deepseek-v4-pro"

// getEnvOr 读环境变量，未设置则返回 fallback。
func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// dangerousTools 是会修改文件系统的工具集合，执行前必须获得用户确认
var dangerousTools = map[string]bool{
	"write_file":  true,
	"rename_file": true,
	"create_file": true,
}

// confirmToolAction 打印危险工具将要执行的内容并阻塞等待用户确认。
// 返回 true 表示确认执行，false 表示取消。
func confirmToolAction(scanner *bufio.Scanner, toolName, argsJSON string) bool {
	fmt.Printf("\n⚠️  即将执行 %s，参数：\n    %s\n", toolName, argsJSON)
	fmt.Print("   确认执行？(y/N): ")
	if !scanner.Scan() {
		return false
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "y" || answer == "yes"
}

// getLlmTools 注册智能体可用的工具
func getLlmTools() map[string]*tools.ToolDef {
	llmTools := make(map[string]*tools.ToolDef)

	listDir := tools.NewListDir()
	readFile := tools.NewReadFile()
	renameFile := tools.NewRenameFile()
	createFile := tools.NewCreateFile()
	writeFile := tools.NewWriteFile()

	llmTools[listDir.Name] = listDir
	llmTools[readFile.Name] = readFile
	llmTools[renameFile.Name] = renameFile
	llmTools[createFile.Name] = createFile
	llmTools[writeFile.Name] = writeFile
	return llmTools
}

// newSaveMemoryTool 创建 save_memory 工具，让模型能把信息写入长期记忆。
// kind=preference 写入"用户稳定偏好"区；kind=summary 追加一条带时间戳的摘要。
func newSaveMemoryTool(memMgr *memory.Manager) *tools.ToolDef {
	return &tools.ToolDef{
		Name:        "save_memory",
		Description: "把需要长期记住的信息写入智能体的长期记忆。当用户明确要求记住某件事、录入记忆、保存偏好时调用。",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"content": map[string]string{
					"type":        "string",
					"description": "要记住的内容，简明扼要",
				},
				"kind": map[string]any{
					"type":        "string",
					"enum":        []string{"preference", "summary"},
					"description": "preference=用户稳定偏好（长期有效）；summary=一次性的事实记录",
				},
			},
			"required": []string{"content", "kind"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			content, _ := args["content"].(string)
			kind, _ := args["kind"].(string)
			if content == "" {
				return "", fmt.Errorf("参数 content 必填")
			}
			if kind == "summary" {
				if err := memMgr.AppendSummary(content); err != nil {
					return "", err
				}
			} else {
				if err := memMgr.AppendPreferences(content); err != nil {
					return "", err
				}
			}
			return "已写入长期记忆", nil
		},
	}
}

// buildOpenaiTools 把工具转成 OpenAI API 要求的格式
func buildOpenaiTools(ts map[string]*tools.ToolDef) []responses.ToolUnionParam {
	openaiTools := make([]responses.ToolUnionParam, 0)
	for _, t := range ts {
		openaiTools = append(openaiTools, t.ToParam())
	}
	return openaiTools
}

// runOnce 执行一轮对话：把用户输入交给 LLM，循环处理工具调用，直到 LLM 给出最终回答。
// scanner 用于危险工具执行前的用户确认；传 nil 则跳过确认（如 MCP 无交互场景）。
// 返回值表示是否正常结束（false 表示 maxLoop 或错误，不应生成摘要）。
func runOnce(ctx context.Context, client openai.Client, session *core.Session, llmTools map[string]*tools.ToolDef, openaiTools []responses.ToolUnionParam, scanner *bufio.Scanner) {
	for realLoop := 0; realLoop <= MaxLoop; realLoop++ {
		resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
			Input: responses.ResponseNewParamsInputUnion{
				OfInputItemList: session.GetMessages(),
			},
			Tools: openaiTools,
			Model: modelName,
		})

		if err != nil {
			var apiErr *openai.Error
			if errors.As(err, &apiErr) {
				fmt.Println("HTTP 状态码:", apiErr.StatusCode)
				fmt.Println("错误类型:", apiErr.Type)
				fmt.Println("原始响应体:", apiErr.RawJSON())
			} else {
				fmt.Println("非 API 错误:", err)
			}
			return
		}

		hasToolCall := false
		for _, item := range resp.Output {
			switch item.Type {
			case "reasoning":
				// 打印模型的思考过程
				for _, summary := range item.Summary {
					fmt.Println("[思考]", summary.Text)
				}
			case "function_call":
				fmt.Printf("[调用工具] %s\n", item.AsFunctionCall().Name)
				hasToolCall = true

				// 把 LLM 的工具调用请求记录到会话
				session.AddMessage(responses.ResponseInputItemUnionParam{
					OfFunctionCall: &responses.ResponseFunctionToolCallParam{
						Arguments: item.AsFunctionCall().Arguments,
						CallID:    item.AsFunctionCall().CallID,
						Name:      item.AsFunctionCall().Name,
					},
				})

				// 执行工具
				toolName := item.AsFunctionCall().Name
				argsJSON := item.AsFunctionCall().Arguments

				// 危险工具执行前阻塞确认
				if dangerousTools[toolName] && scanner != nil {
					if !confirmToolAction(scanner, toolName, argsJSON) {
						toolRe := "用户取消了这次操作。请向用户说明：操作已取消，如需继续请明确授权后再调用。"
						fmt.Printf("[用户取消] %s\n", toolName)
						session.AddMessage(responses.ResponseInputItemUnionParam{
							OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
								Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
									OfString: openai.String(toolRe),
								},
								CallID: openai.String(item.AsFunctionCall().CallID),
							},
						})
						continue
					}
				}

				if v, ok := llmTools[toolName]; ok {
					toolRe, err := v.Call(ctx, argsJSON)
					if err != nil {
						toolRe = fmt.Sprintf("执行出错: %v", err)
					}
					fmt.Printf("[工具结果] %s\n", toolRe)

					// 把工具执行结果返回给 LLM
					session.AddMessage(responses.ResponseInputItemUnionParam{
						OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
							Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
								OfString: openai.String(toolRe),
							},
							CallID: openai.String(item.AsFunctionCall().CallID),
						},
					})
				}
			}
		}

		if !hasToolCall {
			fmt.Println("\n[助手]", resp.OutputText())
			return
		}
	}
	fmt.Println("已达到最大循环次数，对话结束。")
}

// generateSessionSummary 在会话即将结束时被调用，拉一次 LLM 对整个 session 做汇总。
// 失败只打印原因，不阻塞主流程退出。
func generateSessionSummary(ctx context.Context, client openai.Client, session *core.Session) string {
	msgs := session.GetMessages()

	summaryCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	resp, err := client.Responses.New(summaryCtx, responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: append([]responses.ResponseInputItemUnionParam{
				responses.ResponseInputItemParamOfMessage(
					"请对以上整段会话做 2-5 行中文总结，格式："+
						"用户主要诉求 → 做了哪些操作（用了哪些工具）→ 结果如何（成功/待确认/未完成）。"+
						"如果会话里用户明确表达了稳定偏好（如'喜欢深色主题'），请在末尾单独一行列出。"+
						"只写事实，不要写观点。",
					responses.EasyInputMessageRoleUser,
				),
			}, msgs...),
		},
		Model: modelName,
	})
	if err != nil {
		fmt.Println("[记忆] 摘要生成失败:", err)
		return ""
	}
	return strings.TrimSpace(resp.OutputText())
}

func main() {
	// 以 MCP 服务器模式运行：my_agent.exe mcp-server
	// 通过 stdio 对外提供 MCP 服务，供 MCP 客户端（包括本智能体自己）连接
	if len(os.Args) > 1 && os.Args[1] == "mcp-server" {
		if err := mcpserver.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "MCP 服务器错误: %v\n", err)
			os.Exit(1)
		}
		return
	}

	session := core.NewSession()

	// 加载技能：扫描 skills 目录，技能清单会注入系统提示词
	skillList, err := skills.LoadAll("skills")
	if err != nil {
		fmt.Printf("[Skill] 加载技能失败: %v\n", err)
	}
	skillReg := skills.NewRegistry(skillList)

	// 初始化长期记忆（独立于 skills 包，全局唯一）
	memMgr, err := memory.New("memory/summaries.md")
	if err != nil {
		fmt.Printf("[Memory] 初始化失败: %v\n", err)
		memMgr = nil
	}

	// 系统提示词
	systemPrompt := "你是一个本地文件小管家智能体，负责帮用户管理本地工作区的文件。" +
		"你可以通过工具列出目录、读取文件内容、重命名文件、创建新文件、向文件写入内容。" +
		"所有操作都被限制在工作区目录内。请用简洁的中文回答用户，操作前先说明你要做什么。" +
		"\n\n## 安全守则（必须遵守）\n" +
		"以下操作属于文件系统修改：write_file、rename_file、create_file。\n" +
		"执行这些操作前，必须先向用户展示完整的变更计划（改什么、怎么改、改后会是什么样），" +
		"等用户明确确认后再调用工具。不要一次偷偷调多个写操作工具——一次一个，等用户确认一个再调下一个。\n" +
		"即使用户说可以直接改，每步修改仍然会在执行前被拦截确认，请按提示输入 y 或 n。" +
		"\n\n## 记忆机制\n" +
		"你有长期记忆，由系统在会话结束时自动归档，会话开始时相关内容已注入上文。\n" +
		"当用户要求记住某事、录入记忆或保存偏好时，调用 save_memory 工具写入；" +
		"不要在 workspace 里创建 memory.md 等文件来记录——那不是长期记忆，程序不会读取它。"

	// 注入长期记忆（如果有）
	if memMgr != nil {
		if mem := memMgr.LoadForPrompt(); mem != "" {
			systemPrompt += "\n\n## 已记住的用户偏好和最近会话\n" + mem
		}
	}

	if section := skillReg.PromptSection(); section != "" {
		systemPrompt += "\n\n" + section
	}
	session.AddMessage(
		responses.ResponseInputItemParamOfMessage(
			systemPrompt,
			responses.EasyInputMessageRoleSystem),
	)

	llmTools := getLlmTools()

	// 注册记忆写入工具（依赖 memMgr，需在其初始化之后）
	if memMgr != nil {
		saveMem := newSaveMemoryTool(memMgr)
		llmTools[saveMem.Name] = saveMem
	}

	ctx := context.Background()

	// 从环境变量读取配置（避免硬编码）
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "❌ 缺少环境变量 DEEPSEEK_API_KEY")
		fmt.Fprintln(os.Stderr, "   请先设置：$env:DEEPSEEK_API_KEY='你的key'")
		fmt.Fprintln(os.Stderr, "   或复制 .env.example 为 .env 后填写")
		os.Exit(1)
	}
	baseURL := getEnvOr("DEEPSEEK_BASE_URL", "https://api.deepseek.com")
	modelName = getEnvOr("DEEPSEEK_MODEL", modelName)

	// 强制直连：Transport 的 Proxy 设为 nil，忽略 HTTP_PROXY/HTTPS_PROXY 环境变量，
	// 避免本地代理(如 Clash 127.0.0.1:7890)拦截导致请求失败。DeepSeek 是国内服务，无需代理。
	noProxyClient := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   5 * time.Minute,
	}
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
		option.WithHTTPClient(noProxyClient),
		option.WithRequestTimeout(5*time.Minute),
	)

	// 接入 MCP 服务器：读取 mcp.json，连接服务器并发现其工具
	mgr := mcpclient.NewManager()
	defer mgr.Close()
	mcpCfgs, err := mcpclient.LoadConfig("mcp.json")
	if err != nil {
		fmt.Printf("[MCP] 读取配置失败: %v\n", err)
	}
	_ = mgr.ConnectAll(ctx, mcpCfgs)
	mcpTools := mgr.Tools()
	for name, td := range mcpTools {
		llmTools[name] = td
	}

	// 有技能时才注册 load_skill 工具
	skillToolCount := 0
	if skillReg.Len() > 0 {
		loadSkill := skillReg.LoadTool()
		llmTools[loadSkill.Name] = loadSkill
		skillToolCount = 1
	}

	// 合并完 MCP 工具后再生成发给 LLM 的工具清单
	openaiTools := buildOpenaiTools(llmTools)

	fmt.Println("===== 本地文件小管家智能体 =====")
	fmt.Printf("工作区目录: %s\n", tools.WorkspaceDir)
	fmt.Printf("已注册工具 %d 个（内置 %d 个 + MCP %d 个 + 技能 %d 个）\n",
		len(llmTools), len(llmTools)-len(mcpTools)-skillToolCount, len(mcpTools), skillToolCount)
	if skillReg.Len() > 0 {
		fmt.Printf("已加载技能 %d 个:\n", skillReg.Len())
		for _, s := range skillReg.List() {
			fmt.Printf("  [Skill] %s - %s\n", s.Name, s.Description)
		}
	}
	if memMgr != nil {
		if st := memMgr.Stats(); st.Bytes > 0 {
			fmt.Printf("长期记忆 %d B（%d 条摘要，有偏好：%v）→ %s\n",
				st.Bytes, st.SummaryCount, st.HasPrefs, st.Path)
		}
	}
	mcpNames := make([]string, 0, len(mcpTools))
	for name := range mcpTools {
		mcpNames = append(mcpNames, name)
	}
	sort.Strings(mcpNames)
	for _, name := range mcpNames {
		fmt.Printf("  [MCP] %s - %s\n", name, mcpTools[name].Description)
	}
	fmt.Println("输入你想做的事（例如：查看当前目录有哪些文件），输入 quit 退出。")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n你> ")
		if !scanner.Scan() {
			break
		}
		userInput := scanner.Text()
		if userInput == "quit" || userInput == "exit" {
			break
		}
		if userInput == "" {
			continue
		}

		session.AddMessage(
			responses.ResponseInputItemParamOfMessage(userInput, responses.EasyInputMessageRoleUser),
		)
		runOnce(ctx, client, session, llmTools, openaiTools, scanner)
	}

	// 会话即将结束（正常退出或 Ctrl+D），对整个 session 生成一条汇总
	if memMgr != nil {
		fmt.Println("\n[记忆] 正在整理本次会话...")
		if summary := generateSessionSummary(ctx, client, session); summary != "" {
			if err := memMgr.AppendSummary(summary); err != nil {
				fmt.Printf("[Memory] 写入会话摘要失败: %v\n", err)
			} else {
				fmt.Printf("[记忆] 本次会话已归档\n")
			}
		}
	}
	fmt.Println("再见！")
}
