# AGENTS.md — new-api 仓库操作与开发规范

适用于全仓库；子目录 `AGENTS.md` 对其目录优先。具体工具版本与脚本以各模块清单和 CI 为准。

## 1. 项目概览与架构

多上游 AI API 网关，提供统一模型接口、用户鉴权、配额计费、速率限制与管理后台。

- **根后端**：Go 1.26，基于 Gin + GORM；主要目录为 `router/`、`middleware/`、`controller/`、`service/`、`model/`、`relay/`。
- **独立 Go 模块**：`relaykit/` 负责协议 DTO 与转换（Go 1.25.1）；`tokenkit/` 负责文本与多模态 Token 估算（Go 1.26）。
- **插件系统**：`plugins/tasks/` 为 JS 任务插件，运行时位于 `pkg/jsplugin/`，使用 moejs。
- **前端控制台**：`web/` 使用 React 19、TypeScript、Bun、Rsbuild 2、TanStack、Base UI、Tailwind CSS 4；详见 `web/AGENTS.md`。

## 2. 常用操作命令

Go 版本遵循各模块 `go.mod`；Bun 使用 CI 的 1.4.4。以下命令使用 Bash，Windows 可使用 Git Bash / WSL。

`main.go` 嵌入 `web/dist`。未构建前端时，单独检查或编译根模块可先创建占位文件；发布必须使用真实前端构建产物：

```bash
mkdir -p web/dist && touch web/dist/index.html
```

后端命令从仓库根目录执行：

```bash
export GOWORK=off
go vet ./...
go build ./...
make test                           # 根模块（不含 main 包）、relaykit、tokenkit 测试
(cd relaykit && go vet ./... && go build ./... && go test ./...)
(cd tokenkit && go vet ./... && go build ./... && go test ./...)
make dev                            # Docker 后端与依赖服务 + 前端开发服务器
```

前端命令在 `web/` 执行：

```bash
cd web
bun install --frozen-lockfile
bun run dev                         # 开发服务器
bun run build                       # 生产构建
bun run typecheck                   # bun check -b，要求匹配的 Bun 版本
bun run lint
bun run test
bun run i18n:sync
```

## 3. 核心契约与开发规范

- **JSON 封装**：根模块编解码统一使用 `common.Marshal`、`common.Unmarshal`、`common.UnmarshalJsonStr`、`common.DecodeJson`、`common.GetJsonType`；`encoding/json` 可仅作 `RawMessage`、`Number` 等类型引用。`relaykit/` 使用 `kitutil.*`。
- **数据库兼容**：支持 SQLite、MySQL >=5.7.8、PostgreSQL >=9.6；优先 GORM，`model/` 内行锁沿用 `lockForUpdate(tx)`（未导出，自有包需要时再补最小导出接点），原生 SQL 须处理方言与保留字差异。
- **Relay DTO**：请求中可选标量使用带 `omitempty` 的指针，保留显式 `0`、`false` 与未提供字段的区别。
- **模块独立**：`relaykit/`、`tokenkit/` 不得依赖根模块；`tokenkit/` 不得依赖 `relaykit/`。各自在 `GOWORK=off` 下独立构建验证。
- **Go 与测试**：修改 Go 文件运行 `gofmt`；测试优先扩充已有文件，以 `testify/require` 作致命断言、`testify/assert` 作非致命值断言，保护真实行为，避免跨层重复测试。
- **前端**：遵循 `web/AGENTS.md`；修改 UI 前读取项目 `shadcn-ui` 技能并检索现有组件与用例。界面语言码传入 `Intl.*` / `toLocale*` 前经 `toIntlLocale` 转换；普通数字与金额复用现有格式化工具。
- **国际化**：前端文案使用 `t()` 并补齐七种语言；控制台 API 使用 `common.NewMessage` / `ApiErrorT` / `ApiSuccessT`，面向 AI 客户端的消息使用后端 `i18n`，服务端日志使用 `common.LogText`。
- **计费门禁**：涉及定价、用量推导、计费倍率请求字段、额度预扣/结算/退款时，必须先完整阅读 `.agents/rules/billing.md`。
- **任务插件**：先读 `docs/plugin-api/v1.md`；修改契约同步核对 schema 与类型声明。数值计费字段描述写为「计费标的 + 单价」，单位写入 `unit`。

## 4. 自有扩展与上游同步

- **目标**：兼顾现有功能改造与持续合并上游 new-api tag；优先隔离新增逻辑、集中必要接入，不以“上游零改动”限制需求。目录约定随真实功能落地，不预建空框架。
- **后端归属**：独立新增业务优先放 `internal/mowan/<feature>/`，默认复用上游能力；现有业务包可按需显式调用自有模块，保持 Go 依赖无环。确有依赖环时，在调用方定义最小接口/回调，由启动层装配；不为单次调用强建抽象。保留现有 Go 模块路径及两个独立模块的隔离。
- **接口接入**：新增独立管理接口用 `/api/mowan/`，在 `router/mowan-router.go` 从现有 `/api` 路由组挂载；改造已有接口优先保留原 URL 与契约。接口须继承审计、限流和请求清理，在 `SetPluginRouter` 快照前完成注册；同前缀的独立 Gin 分组不会自动继承中间件。
- **权限接入**：复用登录鉴权、Casbin 和 `handlePermissionRoute`（同时登记 PAT scope）；权限资源须在每实例启动的 `authz.Init` 前注册，不受迁移节点条件限制。菜单可见性不能替代路由权限、服务端授权及数据归属检查。
- **数据归属**：扩展表用 `mowan_` 前缀，通过关联表保存附加信息，优先避免修改上游核心表。自有迁移在 `model.InitDB` 成功后由启动装配层显式执行，遵循 `common.IsMasterNode`、失败阻止启动；多主部署串行迁移，避免模型包与自有模块互相导入。
- **前端归属**：独立新增功能优先放 `web/src/features/mowan/<feature>/`，薄路由放 `web/src/routes/_authenticated/console/mowan/`，URL 用插件保留空间 `/console/mowan/`。现有页面允许原地修改、组合或替换自有组件，按改造范围选择，优先保留原路由和复用数据/权限能力；不强制复制整页或迁往新 URL。路由树由工具重新生成，禁止手改 `web/src/routeTree.gen.ts`。
- **复用机制**：符合模型任务协议的适配优先用现有 JS Task Plugin，自有 key 用 `mowan-` 前缀；周期任务复用 `RegisterSystemTaskHandler`。管理业务仍用 Go 模块，不另建通用插件框架或复制鉴权、计费、协议转换实现。
- **共享改动**：允许按需改造现有页面、布局和业务流程；简单改动留在原处，可独立维护的大块自有逻辑再拆出，避免仅为隔离而复制上游代码。提交/PR 记录改造目的、上游接触点与升级回归点；业务改造与上游兼容补丁分开提交，自有部署配置用独立覆盖文件。
- **分支职责**：`main` 是产品稳定集成主线，接收验证通过的自有功能与上游更新；功能从 `main` 创建 `feat/<功能名>`，上游同步从 `main` 创建 `sync/upstream-<tag>`。两类改动分别开发、验证并合回 `main`，合入前结合最新主线回归；暂不设长期 `develop`。
- **Git 策略**：保留 fork 历史；上游同步分支 merge 明确的上游 tag，再以普通 merge 合入主线，保留上游提交祖先关系，不 squash 同步历史、不 rebase 已共享主线。在验证通过的 `main` 提交上打自有 `mowan-v*` 发布 tag，与上游 tag 分开，记录上游 tag 与 commit。
- **升级验证**：复核接入点及上游接口/配置变化，运行后端三模块 vet/build/test、前端 typecheck/lint/test/build，并回归自有业务。数据库/ORM/迁移受影响时，用真实三数据库验证新库、存量升级与重复启动；文本无冲突不能代替行为验证，未执行检查须说明。
- **发布回退**：发布关联自有版本、上游 tag/commit 和镜像；升级前备份数据。代码回退须核对 schema 兼容性，Git revert 或关闭功能开关不能代替数据库回滚。

## 5. 边界与红线规则

- **文档限制**：未经明确要求，不在 `docs/` 新增文档，不在 `plugins/` 下生成说明文档。
- **极简改动**：优先复用、提前返回与清晰分支；不为未知需求建立框架，不进行无关重构。
