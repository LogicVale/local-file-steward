package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/openai/openai-go/v3"
)

// NewWriteFile 向已存在的文件写入内容（覆盖原有内容）。
// 如果文件不存在，会自动创建。
func NewWriteFile() *ToolDef {
	return &ToolDef{
		Name:        "write_file",
		Description: "向工作区内的文件写入文本内容，会覆盖原有内容。如果文件不存在则自动创建。",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]string{
					"type":        "string",
					"description": "要写入的文件路径（相对于工作区）",
				},
				"content": map[string]string{
					"type":        "string",
					"description": "要写入的文本内容",
				},
			},
			"required": []string{"path", "content"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)
			content, _ := args["content"].(string)
			if path == "" {
				return "", fmt.Errorf("参数 path 必填")
			}

			full, err := safeJoin(path)
			if err != nil {
				return "", err
			}

			// 确保父目录存在
			if err := os.MkdirAll(dirOf(full), 0755); err != nil {
				return "", err
			}

			if err := os.WriteFile(full, []byte(content), 0644); err != nil {
				return "", err
			}
			return fmt.Sprintf("已向 %s 写入 %d 个字符", path, len([]rune(content))), nil
		},
	}
}
