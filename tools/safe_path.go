package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorkspaceDir 智能体允许操作的根目录，所有文件操作都被限制在该目录内，
// 防止误删/误改工作区之外的文件。
var WorkspaceDir string

func init() {
	// 默认把工作目录设在程序运行目录下的 workspace 文件夹
	abs, err := filepath.Abs("workspace")
	if err != nil {
		panic(err)
	}
	WorkspaceDir = abs
	// 自动创建工作目录（如果不存在）
	_ = os.MkdirAll(WorkspaceDir, 0755)
}

// safeJoin 把用户传入的相对路径拼接到 WorkspaceDir 下，
// 并校验最终路径没有越出工作区（防止 ../ 跳出目录）。
func safeJoin(userPath string) (string, error) {
	if userPath == "" {
		userPath = "."
	}
	// 拒绝绝对路径，避免绕过工作区限制
	if filepath.IsAbs(userPath) {
		return "", fmt.Errorf("不允许使用绝对路径，请传入相对于工作区的路径")
	}
	full := filepath.Join(WorkspaceDir, userPath)
	clean := filepath.Clean(full)

	root := filepath.Clean(WorkspaceDir)
	// 允许等于根目录，或在根目录内部
	if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		return "", fmt.Errorf("路径越界，不允许访问工作区之外的文件: %s", userPath)
	}
	return clean, nil
}

// dirOf 返回文件路径的父目录，如果路径本身就是根目录则返回自身
func dirOf(path string) string {
	dir := filepath.Dir(path)
	if dir == "." {
		return WorkspaceDir
	}
	return dir
}
