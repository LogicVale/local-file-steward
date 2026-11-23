---
name: html-page-creator
description: 创建优美的单文件 HTML 页面（内联 CSS + JS），包含现代设计风格、响应式布局和流畅交互
---

# 优美 HTML 页面创建技能

## 核心原则
生成**单个 .html 文件**，所有 CSS 和 JS 内联在 `<head>` 或 `<body>` 底部，不依赖外部资源（CDN 字体除外）。文件即用即开，无需构建。

## 设计规范

### 1. Design Tokens（必须在 :root 中声明）
```css
:root {
  --color-primary: #6366f1;     /* 主色 */
  --color-surface: #ffffff;      /* 背景色 */
  --color-text: #0f172a;         /* 正文色 */
  --color-muted: #64748b;        /* 次要文本 */
  --radius-sm: 6px;
  --radius-md: 12px;
  --radius-lg: 20px;
  --shadow-sm: 0 1px 3px rgba(0,0,0,0.08);
  --shadow-md: 0 8px 24px rgba(0,0,0,0.12);
  --shadow-lg: 0 24px 48px rgba(0,0,0,0.16);
  --ease: cubic-bezier(0.4, 0, 0.2, 1);
}
```

### 2. 视觉风格
- **配色**：从用户描述或 memory.md 偏好中选；无偏好时用浅色调 + 单一强调色
- **字体**：系统字体栈 `system-ui, -apple-system, "Segoe UI", sans-serif`；需要展示字体效果时引入 Google Fonts
- **间距**：4px 基线网格，常用值 4/8/12/16/24/32/48
- **圆角**：卡片用 12-20px，按钮用 6-10px，标签用 pill 形状
- **阴影**：层级分明，hover 比常态重一级
- **动效**：过渡用 `var(--ease)`，时长 200-400ms，只用 transform/opacity（性能好）

### 3. 交互反馈（必须）
- 所有可点击元素统一 `a{text-decoration:none; color:inherit}` 去除浏览器默认下划线
- hover 态：轻微位移（translateY(-2px)）+ 阴影加深 + 0.2s 过渡
- focus-visible：2px 描边 `outline: 2px solid var(--color-primary); outline-offset: 2px`
- 卡片/按钮：hover 微浮动、点击微回弹（scale 0.98）

### 4. 响应式
- 移动端优先（@media min-width）
- 断点：640px / 768px / 1024px / 1440px
- 图片/视频：`max-width: 100%; height: auto`

## 结构模板
```html
<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>{标题}</title>
  <style>
    /* Design Tokens */
    /* Reset / Base */
    /* Layout */
    /* Components */
    /* Animations */
    @media (...) { /* Responsive */ }
  </style>
</head>
<body>
  {页面内容}
  <script>
    /* 交互逻辑，无则省略 */
  </script>
</body>
</html>
```

## 工作流程

1. 明确页面类型（落地页、仪表盘、个人主页、活动页、文章）和核心内容
2. 读取 memory.md 确认用户偏好（配色、风格禁忌、喜欢的布局）
3. 选择版式系统：
   - **落地页**：Hero + 特性卡网格 + CTA 双按钮
   - **仪表盘**：双栏（侧栏 + 主区）+ 数据卡片矩阵 + 图表占位
   - **个人主页**：单栏长滚 + 分章节（关于 / 项目 / 联系）
   - **活动页**：Hero + 倒计时 / 议程 / 报名表单
4. 写 HTML 骨架 → 填 Design Tokens → 写 Components → 加交互 → 响应式调整
5. 交付前自查：在浏览器打开确认无破版

## 交付约束
- 文件大小控制在 50KB 内（纯 HTML），超过则拆分
- 内容用中文时 `<html lang="zh-CN">`
- 图片用用户提供的路径或占位，不要生成虚拟图片
