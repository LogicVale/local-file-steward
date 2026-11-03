package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/openai/openai-go/v3"
)

// NewListDir 列出指定目录下的文件和子目录
func NewListDir() *ToolDef {
	return &ToolDef{
		Name:        "list_dir",
		Description: "列出工作区内指定目录下的所有文件和子目录，默认列出工作区根目录。",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]string{
					"type":        "string",
					"description": "可选，要列出的目录路径（相对于工作区），默认为根目录",
				},
			},
			"required": []string{},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			path, _ := args["path"].(string)

			full, err := safeJoin(path)
			if err != nil {
				return "", err
			}

			info, err := os.Stat(full)
			if err != nil {
				return "", err
			}
			if !info.IsDir() {
				return "", fmt.Errorf("%s 不是一个目录", path)
			}

			entries, err := os.ReadDir(full)
			if err != nil {
				return "", err
			}

			var items []map[string]any
			for _, e := range entries {
				item := map[string]any{
					"name":   e.Name(),
					"is_dir": e.IsDir(),
				}
				if fi, err := e.Info(); err == nil {
					item["size"] = fi.Size()
				}
				items = append(items, item)
			}

			b, err := json.Marshal(items)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	}
}
