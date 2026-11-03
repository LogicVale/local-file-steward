package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/openai/openai-go/v3"
)

// NewReadFile 读取指定文件的文本内容
func NewReadFile() *ToolDef {
	return &ToolDef{
		Name:        "read_file",
		Description: "读取工作区内指定文件的文本内容。",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]string{
					"type":        "string",
					"description": "要读取的文件路径（相对于工作区）",
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

			info, err := os.Stat(full)
			if err != nil {
				return "", err
			}
			if info.IsDir() {
				return "", fmt.Errorf("%s 是一个目录，不是文件", path)
			}

			data, err := os.ReadFile(full)
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	}
}
