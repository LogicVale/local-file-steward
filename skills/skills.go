// Package skills 为智能体提供技能（Skill）能力。
//
// 一个技能就是 skills/<目录名>/SKILL.md 文件，开头用 YAML frontmatter
// 声明 name 和 description，其后是技能正文（具体做法、步骤、注意事项）。
//
// 采用渐进式加载：启动时只扫描出技能的名称和描述注入系统提示词，
// 等模型判断当前任务与某个技能匹配时，再通过 load_skill 工具加载完整正文，
// 避免把所有技能全文一次性塞进上下文。
//
// 长期记忆见独立的 memory 包（memory/memory.go）。
package skills

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"my_agent/tools"

	"github.com/openai/openai-go/v3"
)

const SkillFile = "SKILL.md"

// Skill 一个已加载的技能
type Skill struct {
	Name        string // frontmatter name，缺省用目录名
	Description string // 模型靠它判断何时使用
	Dir         string // 技能所在目录，引用同目录资源
	Body        string // 去掉 frontmatter 后的正文
}

// LoadAll 扫描 root 下所有子目录中的 SKILL.md 并加载技能。
func LoadAll(root string) ([]*Skill, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var list []*Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		s, err := loadOne(dir, filepath.Join(dir, SkillFile))
		if err != nil {
			fmt.Printf("[Skill] 跳过 %s: %v\n", dir, err)
			continue
		}
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

func loadOne(dir, path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	meta, body := splitFrontmatter(string(data))
	name := meta["name"]
	if name == "" {
		name = filepath.Base(dir)
	}
	desc := meta["description"]
	if desc == "" {
		return nil, fmt.Errorf("frontmatter 缺少 description")
	}
	return &Skill{Name: name, Description: desc, Dir: dir, Body: body}, nil
}

func splitFrontmatter(content string) (map[string]string, string) {
	meta := make(map[string]string)
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")

	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	if start >= len(lines) || strings.TrimSpace(lines[start]) != "---" {
		return meta, strings.TrimSpace(content)
	}

	end := -1
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return meta, strings.TrimSpace(content)
	}

	for _, line := range lines[start+1 : end] {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		meta[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return meta, strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
}

// ========== 核心出口 ==========

func (s *Skill) fullText() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# 技能：%s\n\n%s", s.Name, s.Body)

	if files := s.resourceFiles(); len(files) > 0 {
		sb.WriteString("\n\n## 技能目录内可读取的文件\n")
		sb.WriteString("需要时可再次调用 load_skill 并传入 file 参数读取：\n")
		for _, f := range files {
			fmt.Fprintf(&sb, "- %s\n", f)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (s *Skill) resourceFiles() []string {
	const maxResourceFiles = 50
	var files []string
	_ = filepath.WalkDir(s.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.Dir, path)
		if err != nil || rel == SkillFile {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		if len(files) >= maxResourceFiles {
			return fs.SkipAll
		}
		return nil
	})
	sort.Strings(files)
	return files
}

func (s *Skill) readFile(rel string) (string, error) {
	rel = filepath.FromSlash(rel)
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("file 必须是技能目录内的相对路径")
	}
	full := filepath.Clean(filepath.Join(s.Dir, rel))
	root := filepath.Clean(s.Dir)
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", fmt.Errorf("路径越界，只能读取技能目录内的文件: %s", rel)
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s 是目录，请指定具体文件", rel)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ========== Registry ==========

type Registry struct {
	list []*Skill
	byID map[string]*Skill
}

func NewRegistry(list []*Skill) *Registry {
	r := &Registry{byID: make(map[string]*Skill, len(list))}
	for _, s := range list {
		if _, ok := r.byID[s.Name]; ok {
			fmt.Printf("[Skill] 技能名重复，已忽略: %s (%s)\n", s.Name, s.Dir)
			continue
		}
		r.byID[s.Name] = s
		r.list = append(r.list, s)
	}
	return r
}

func (r *Registry) List() []*Skill { return r.list }
func (r *Registry) Len() int      { return len(r.list) }

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.list))
	for _, s := range r.list {
		names = append(names, s.Name)
	}
	return names
}

// PromptSection 注入系统提示词的技能清单（只有技能列表，记忆规则在 memory 包里）
func (r *Registry) PromptSection() string {
	if len(r.list) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## 可用技能\n")
	sb.WriteString("当用户任务与下列某个技能匹配时，必须先调用 load_skill 工具加载该技能的完整说明，" +
		"按说明完成任务，不要凭猜测直接执行。\n\n")
	for _, s := range r.list {
		fmt.Fprintf(&sb, "- %s：%s\n", s.Name, s.Description)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// LoadTool 生成 load_skill 工具（加载正文 + 读配套文件）。
// 记忆相关参数已移至独立的 memory 包，不在此处。
func (r *Registry) LoadTool() *tools.ToolDef {
	names := r.Names()
	return &tools.ToolDef{
		Name: "load_skill",
		Description: "加载指定技能的完整说明；也可传入 file 读取该技能目录内的配套文件。可用技能：" + strings.Join(names, "、") + "。",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "要加载的技能名称",
					"enum":        names,
				},
				"file": map[string]any{
					"type":        "string",
					"description": "可选，技能目录内的相对路径（如 reference.md）；不填则返回技能说明正文",
				},
			},
			"required": []string{"name"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			name, _ := args["name"].(string)
			s, ok := r.byID[name]
			if !ok {
				return "", fmt.Errorf("不存在名为 %q 的技能，可用技能：%s", name, strings.Join(names, "、"))
			}
			file, _ := args["file"].(string)
			if strings.TrimSpace(file) == "" {
				return s.fullText(), nil
			}
			return s.readFile(file)
		},
	}
}
