package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/openai/openai-go/v3"
)

// NewCreateFile 创建一个新文件，可以指定初始内容；如果文件已存在则报错。
func NewCreateFile() *ToolDef {
	return &ToolDef{
		Name:        "create_file",
		Description: "在工作区内创建一个新文件，可写入初始内容。如果文件已存在会报错，请改用 write_file。",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]string{
					"type":        "string",
					"description": "要创建的文件路径（相对于工作区）",
				},
				"content": map[string]string{
					"type":        "string",
					"description": "可选，文件的初始内容，默认为空文件",
				},
			},
			"required": []string{"path"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			if path == "" {
				return "", fmt.Errorf("参数 path 必填")
			}

			full, err := safeJoin(path)
			if err != nil {
				return "", err
			}

			// 检查文件是否已存在
			if _, err := os.Stat(full); err == nil {
				return "", fmt.Errorf("文件已存在: %s，如需修改内容请使用 write_file", path)
			}

			content, _ := args["content"].(string)

			// 确保父目录存在
			if err := os.MkdirAll(dirOf(full), 0755); err != nil {
				return "", err
			}

			if err := os.WriteFile(full, []byte(content), 0644); err != nil {
				return "", err
			}
			return fmt.Sprintf("已创建文件 %s", path), nil
		},
	}
}
