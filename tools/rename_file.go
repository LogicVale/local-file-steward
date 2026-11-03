package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/openai/openai-go/v3"
)

// NewRenameFile 重命名或移动文件/目录
func NewRenameFile() *ToolDef {
	return &ToolDef{
		Name:        "rename_file",
		Description: "重命名工作区内的文件或目录，也可以用来移动文件位置。",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"old_path": map[string]string{
					"type":        "string",
					"description": "原文件/目录的路径（相对于工作区）",
				},
				"new_path": map[string]string{
					"type":        "string",
					"description": "新的文件/目录路径（相对于工作区）",
				},
			},
			"required": []string{"old_path", "new_path"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			oldPath, _ := args["old_path"].(string)
			newPath, _ := args["new_path"].(string)
			if oldPath == "" || newPath == "" {
				return "", fmt.Errorf("参数 old_path 和 new_path 都必填")
			}

			oldFull, err := safeJoin(oldPath)
			if err != nil {
				return "", err
			}
			newFull, err := safeJoin(newPath)
			if err != nil {
				return "", err
			}

			if _, err := os.Stat(oldFull); err != nil {
				return "", fmt.Errorf("源文件不存在: %s", oldPath)
			}

			if err := os.Rename(oldFull, newFull); err != nil {
				return "", err
			}
			return fmt.Sprintf("已将 %s 重命名为 %s", oldPath, newPath), nil
		},
	}
}
