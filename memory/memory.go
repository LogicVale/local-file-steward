// Package memory 管理智能体的长期记忆，独立于 skills 和 tools。
//
// 所有记忆统一放在一个 Markdown 文件里（默认 memory/summaries.md），分两个区域：
//   ## 会话摘要（每轮对话结束后自动生成）
//   ## 用户稳定偏好（从摘要提炼，或模型主动识别后写入）
//
// 文件有大小上限（默认 8KB），超限后自动压缩最旧的摘要条目。
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	DefaultPath      = "memory/summaries.md"
	maxMemoryBytes   = 8192  // 8KB 上限
	maxSummaryAge    = 20    // 超过 20 条摘要就压缩最旧的
)

// Manager 记忆管理器
type Manager struct {
	path string
}

// New 创建记忆管理器，自动创建目录。path 为空则用默认路径。
func New(path string) (*Manager, error) {
	if path == "" {
		path = DefaultPath
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建记忆目录 %s 失败: %v", dir, err)
	}
	return &Manager{path: path}, nil
}

// Load 读取记忆文件的全部内容（文件不存在返回空串）
func (m *Manager) Load() string {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// LoadForPrompt 返回要注入到 system prompt 的记忆片段。
// 如果有偏好区，把偏好区作为"已记住的用户偏好"注入。
func (m *Manager) LoadForPrompt() string {
	content := m.Load()
	if content == "" {
		return ""
	}

	// 优先提取"用户稳定偏好"章节（如果存在）
	var sb strings.Builder
	if idx := strings.Index(content, "## 用户稳定偏好"); idx != -1 {
		sb.WriteString(content[idx:])
	} else if idx := strings.Index(content, "## 会话摘要"); idx != -1 {
		// 只有摘要没有偏好，取最近几条摘要注入
		summaries := extractRecentSummaries(content, 3)
		sb.WriteString("## 最近会话记录（供参考）\n")
		sb.WriteString(summaries)
	} else {
		sb.WriteString(content)
	}
	return sb.String()
}

// extractRecentSummaries 从记忆文件里提取最近 N 条摘要
func extractRecentSummaries(content string, n int) string {
	var blocks []string
	// 按时间标题切分：### 2026-11-23 20:15
	parts := strings.Split(content, "\n### ")
	for i, p := range parts {
		if i == 0 {
			// 第一块可能包含标题
			continue
		}
		blocks = append(blocks, "### "+strings.TrimSpace(p))
	}
	if len(blocks) == 0 {
		return content
	}
	// 取最近 N 条
	start := len(blocks) - n
	if start < 0 {
		start = 0
	}
	return strings.Join(blocks[start:], "\n\n")
}

// Save 覆写整个记忆文件
func (m *Manager) Save(content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("内容不能为空")
	}
	if len(content)+1 > maxMemoryBytes {
		return fmt.Errorf("内容 %d 字节超过上限 %d，请精简后再写入", len(content), maxMemoryBytes)
	}
	return os.WriteFile(m.path, []byte(content+"\n"), 0644)
}

// AppendSummary 追加一条新的会话摘要，自动带上时间戳。
// 如果文件超限，会自动触发压缩。
func (m *Manager) AppendSummary(summary string) error {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}

	timestamp := time.Now().Format("2006-01-02 15:04")
	newBlock := fmt.Sprintf("\n### %s\n%s", timestamp, summary)

	content := m.Load()
	if content == "" {
		content = "# Agent 长期记忆\n\n## 会话摘要" + newBlock
	} else {
		// 确保有"会话摘要"标题
		if !strings.Contains(content, "## 会话摘要") {
			content = "# Agent 长期记忆\n\n## 会话摘要\n" + content
		}
		content += newBlock
	}

	// 超限自动压缩
	if len(content) > maxMemoryBytes {
		content = compressOldSummaries(content, maxMemoryBytes)
	}

	return m.Save(content)
}

// AppendPreferences 追加/覆写"用户稳定偏好"章节
func (m *Manager) AppendPreferences(prefs string) error {
	prefs = strings.TrimSpace(prefs)
	if prefs == "" {
		return nil
	}

	content := m.Load()
	prefsSection := "## 用户稳定偏好\n" + prefs

	if strings.Contains(content, "## 用户稳定偏好") {
		// 已有偏好区，替换掉
		idx := strings.Index(content, "## 用户稳定偏好")
		content = content[:idx] + prefsSection
	} else {
		// 没有偏好区，在摘要区之前插入
		if strings.Contains(content, "## 会话摘要") {
			content = strings.Replace(content, "## 会话摘要", prefsSection+"\n\n## 会话摘要", 1)
		} else {
			content += "\n\n" + prefsSection
		}
	}

	return m.Save(content)
}

// compressOldSummaries 把最旧的 N 条摘要合并成一条"历史汇总"，释放空间
func compressOldSummaries(content string, targetSize int) string {
	var blocks [][2]string // [时间, 内容]
	parts := strings.Split(content, "\n### ")

	// 第一块可能是标题行，单独处理
	header := parts[0]
	for i := 1; i < len(parts); i++ {
		p := parts[i]
		// 找到标题行结尾（第一个 \n）
		nl := strings.Index(p, "\n")
		var time, body string
		if nl != -1 {
			time = strings.TrimSpace(p[:nl])
			body = strings.TrimSpace(p[nl+1:])
		} else {
			time = strings.TrimSpace(p)
			body = ""
		}
		if time == "" {
			continue
		}
		blocks = append(blocks, [2]string{time, body})
	}

	if len(blocks) <= 5 {
		return content // 太少不用压
	}

	// 把前半部分合并成一条历史汇总，保留后半部分详细记录
	cut := len(blocks) / 2
	var oldParts []string
	for i := 0; i < cut; i++ {
		oldParts = append(oldParts, fmt.Sprintf("- %s: %s", blocks[i][0], blocks[i][1]))
	}
	oldSummary := fmt.Sprintf("\n### %s 以前的会话汇总\n（%d 条旧记录压缩）\n%s",
		blocks[cut-1][0], cut, strings.Join(oldParts, "\n"))

	// 重新组装
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString(oldSummary)
	for i := cut; i < len(blocks); i++ {
		sb.WriteString(fmt.Sprintf("\n\n### %s\n%s", blocks[i][0], blocks[i][1]))
	}

	result := sb.String()
	if len(result) > targetSize {
		// 还超，保留最近 3 条 + 汇总
		if len(blocks)-cut > 3 {
			cut = len(blocks) - 3
			oldParts = oldParts[:0]
			for i := 0; i < cut; i++ {
				oldParts = append(oldParts, fmt.Sprintf("- %s: %s", blocks[i][0], blocks[i][1]))
			}
			result = header +
				fmt.Sprintf("\n### 历史汇总\n（%d 条旧记录压缩）\n%s", cut, strings.Join(oldParts, "\n"))
			for i := cut; i < len(blocks); i++ {
				result += fmt.Sprintf("\n\n### %s\n%s", blocks[i][0], blocks[i][1])
			}
		}
	}
	return result
}

// Stats 返回记忆状态（用于启动时展示）
type Stats struct {
	Path     string
	Bytes    int64
	SummaryCount int
	HasPrefs bool
	Preview  string
}

func (m *Manager) Stats() Stats {
	s := Stats{Path: m.path}
	info, err := os.Stat(m.path)
	if err != nil {
		return s
	}
	s.Bytes = info.Size()

	content := m.Load()
	if content == "" {
		return s
	}

	s.SummaryCount = strings.Count(content, "\n### ")
	s.HasPrefs = strings.Contains(content, "## 用户稳定偏好")

	if len(content) > 100 {
		s.Preview = content[:100] + "…"
	} else {
		s.Preview = content
	}
	return s
}

// ========== 排序时的辅助 ==========
var _ = sort.Strings // 保留引入防止编译警告（实际未用到 sort）
