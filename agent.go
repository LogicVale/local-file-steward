package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"my_agent/core"
	"my_agent/mcpclient"
	"my_agent/mcpserver"
	"my_agent/tools"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

const MaxLoop = 10

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

// buildOpenaiTools 把工具转成 OpenAI API 要求的格式
func buildOpenaiTools(ts map[string]*tools.ToolDef) []responses.ToolUnionParam {
	openaiTools := make([]responses.ToolUnionParam, 0)
	for _, t := range ts {
		openaiTools = append(openaiTools, t.ToParam())
	}
	return openaiTools
}

// runOnce 执行一轮对话：把用户输入交给 LLM，循环处理工具调用，直到 LLM 给出最终回答
func runOnce(ctx context.Context, client openai.Client, session *core.Session, llmTools map[string]*tools.ToolDef, openaiTools []responses.ToolUnionParam) {
	for realLoop := 0; realLoop <= MaxLoop; realLoop++ {
		resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
			Input: responses.ResponseNewParamsInputUnion{
				OfInputItemList: session.GetMessages(),
			},
			Tools: openaiTools,
			Model: "deepseek-v4-pro",
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
				if v, ok := llmTools[item.AsFunctionCall().Name]; ok {
					toolRe, err := v.Call(ctx, item.AsFunctionCall().Arguments)
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
			// 没有工具调用，说明 LLM 已经给出最终回答
			fmt.Println("\n[助手]", resp.OutputText())
			return
		}
	}
	fmt.Println("已达到最大循环次数，对话结束。")
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

	// 系统提示词：定义智能体的身份和行为
	session.AddMessage(
		responses.ResponseInputItemParamOfMessage(
			"你是一个本地文件小管家智能体，负责帮用户管理本地工作区的文件。"+
				"你可以通过工具列出目录、读取文件内容、重命名文件、创建新文件、向文件写入内容。"+
				"所有操作都被限制在工作区目录内。请用简洁的中文回答用户，操作前先说明你要做什么。",
			responses.EasyInputMessageRoleSystem),
	)

	llmTools := getLlmTools()

	ctx := context.Background()
	client := openai.NewClient(
		option.WithAPIKey("sk-6bfb8d3d3c7445e29d0dc735a2fd44db"),
		option.WithBaseURL("https://api.deepseek.com"),
		option.WithRequestTimeout(30*time.Second),
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

	// 合并完 MCP 工具后再生成发给 LLM 的工具清单
	openaiTools := buildOpenaiTools(llmTools)

	fmt.Println("===== 本地文件小管家智能体 =====")
	fmt.Printf("工作区目录: %s\n", tools.WorkspaceDir)
	fmt.Printf("已注册工具 %d 个（内置 %d 个 + MCP %d 个）\n",
		len(llmTools), len(llmTools)-len(mcpTools), len(mcpTools))
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
			fmt.Println("再见！")
			break
		}
		if userInput == "" {
			continue
		}

		// 把用户输入加入会话
		session.AddMessage(
			responses.ResponseInputItemParamOfMessage(userInput, responses.EasyInputMessageRoleUser),
		)
		// 执行一轮
		runOnce(ctx, client, session, llmTools, openaiTools)
	}
}
