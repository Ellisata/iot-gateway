# Claude 项目规范 (claude.md)

## 项目概述
- **名称**: iot-admin-ui
- **技术栈**: Vue 3 (Composition API) + Vite + Tailwind CSS + Element Plus + Axios + Vue Router 
- **目标**: 构建现代、响应式、可维护的企业级前端应用

## 环境要求
- Node.js >= 22.12.0
- npm >= 9.0.0 （随 Node.js 安装）
- 现代浏览器（Chrome、Firefox、Edge 最新两版）

## 目录结构（关键部分）
src/
├── api/ # API 请求模块（按模块划分）
│ ├── modules/
│ └── index.js # 统一导出及 axios 实例配置
├── assets/ # 静态资源（图片、字体等）
├── components/ # 通用 UI 组件
├── composables/ # 组合式函数（Vue composables）
├── layouts/ # 布局组件
├── router/ # Vue Router 配置
├── store/ # Pinia 状态管理（如使用）
├── styles/ # 全局样式（含 Tailwind 自定义）
├── utils/ # 工具函数（如日期格式化、校验等）
├── views/ # 页面级组件
├── App.vue
└── main.js


## 编码规范（摘要）

### JavaScript / Vue
- 使用 **Vue 3 Composition API**（`<script setup>` 语法）
- 组件命名：**PascalCase**（文件名与组件名一致）
- Props 定义必须完整（类型、默认值、校验）
- 优先使用 `ref` / `reactive`，避免 `this` 访问
- 组合式函数以 `use` 开头（如 `useUser`）

### CSS / Tailwind
- 优先使用 Tailwind 工具类，仅在复杂交互或覆盖第三方样式时编写自定义 CSS
- 自定义样式放入 `src/styles/`，并使用 `@apply` 指令复用
- 移动优先响应式设计（`sm:`、`md:` 等）

### API 请求（Axios）
- 所有请求放在 `src/api/`，按业务模块拆分
- 统一错误处理、请求/响应拦截（在 `api/index.js` 中配置）
- 环境变量 `VITE_API_BASE_URL` 作为 baseURL

### Git 提交规范（Conventional Commits）
- 类型：`feat`、`fix`、`docs`、`style`、`refactor`、`perf`、`test`、`chore`
- 示例：`feat: 添加用户登录页面`
- 提交前运行 `npm run lint` 和 `npm run test`（若配置）

## 开发流程
1. 从 `main` 分支拉取最新代码
2. 创建功能分支：`feature/xxx` 或 `fix/xxx`
3. 本地启动：`npm dev`（默认端口 3000）
4. 构建生产：`npm build`
5. 提交 MR/PR 前，确保通过 ESLint 和 Prettier（若有配置）

## AI 辅助约定（用于 Claude 等）
- 当用户请求生成代码时，优先使用本规范约定的技术和风格
- 新增依赖需先评估是否与现有技术栈冲突
- 文档更新需同步修改 `claude.md` 或 README
- 遇到不确定的 API 用法，优先查阅官方最新文档：
  - [Vue 3](https://vuejs.org/)
  - [Tailwind CSS](https://tailwindcss.com/)
  - [Element Plus](https://element-plus.org/)
  - [Vite](https://vitejs.dev/)

## 性能与可访问性
- 使用 Vite 的代码分割和懒加载（路由级别）
- 图片使用 WebP 格式，并设置 `loading="lazy"`
- 为交互元素提供 `aria-label` 等 ARIA 属性
- 避免在模板中直接使用复杂表达式，移至计算属性或方法

## 测试策略（推荐）
- 单元测试：Vitest（与 Vite 集成）
- 组件测试：Vue Test Utils + Vitest
- E2E 测试：Playwright 或 Cypress
- 测试文件放在 `__tests__` 目录或与源文件同级的 `*.spec.js`

---

> **更新日期**: 2026-07-03  
> **维护者**: 开发团队  
> **注意**: 本文件是项目的“宪法”，任何技术选型或架构变更需经过评审并同步更新本文档。