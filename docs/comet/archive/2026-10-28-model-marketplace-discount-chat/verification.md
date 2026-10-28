---
generated_from_state_version: 19
---

# 验证

## 当前结果

- 结果: **已归档**
- 验证情况: **已完成检查，验证结果已确认**
- 目标周期: 2
- 迭代: 2
- 验证器尝试次数: 1
- 完成时间: 2026-10-28T07:12:48.151Z
- 摘要: 通过：全部A1-A6由新独立Verifier逐项裁定。前轮A2极小/near1数值失真、A3真实Seedance误入口、A6法俄越逗号精度三类Blocker在当前候选均未复现，已补本轮Chrome业务证据并复用匹配候选的Runtime检查。浏览器mock已清理、真实目录恢复、证据导出并停止会话；验收结论不等于已accept/archive。

## 验收

| 编号 | 结果 | 来源 | 验收项 | 原因 |
| --- | --- | --- | --- | --- |
| A1 | passed | brief.md | A1：卡片与列表在所有分组时按模型启用且访问者可用分组的最低有效倍率展示折扣，0.3、0.5、1显示“低至3折”；选定0.5分组时显示该组“5折”，切回所有分组恢复“低至3折”。不得使用无权限或模型未启用分组，不叠加充值或汇率。 | 独立审查web/src/features/pricing/lib/model-helpers.ts折扣交集逻辑，并在本轮Chrome对GET /api/pricing隔离mock：模型启用g3/g5/g1/forbidden，访问者仅可用g3/g5/g1，另有模型未启用disabled；倍率0.3/0.5/1、forbidden0.01、disabled0.02。卡片和表格所有分组均最低3折，切g5均确切5折，切回所有分组恢复最低3折，未从无权限或未启用组取折扣。对应当前候选Runtime折扣回归exit0复用。 |
| A2 | passed | brief.md | A2：当前展示倍率为0显示“免费”；0<倍率<1显示准确折数（所有分组用“低至”，指定分组用确切折数）；倍率>=1、缺失、无效或负数隐藏折扣。不得把缺失或极小正倍率显示为免费或0折，也不得把小于1的倍率舍入成10折。 | 本轮Chrome卡片及表格独立观测：ratio0.000001显示低至0.00001折；0.99999999999999显示9.9999999999999折；0.9999999999999999显示9.999999999999998折；未再出现0折、10折、固定9.9999或0.0001%。英文near1显示Up to 9.99200722163E-13% off；ratio1e-17显示Pay from 1E-15%，保留真实非零支付比例而非100%off。ratio0显示免费，missing/negative不显示优惠。代码model-discount-badge.tsx:57-90保留数值域判断，不反解析Intl。当前候选组件极值回归收据exit0复用。 |
| A3 | passed | brief.md | A3：仅游乐场支持的对话模型提供入口；卡片右上角现有复制与对话图标交换位置，视觉顺序为复制在前、对话在后，列表相邻操作保持一致。进入游乐场选中目标模型；详情和复制功能保留，不发送消息、不清空聊天。 | 本轮真实目录43模型，搜索seed独立核对四个Seedance视频与两个Seedream图片，尽管目录含泛化openai/response/anthropic/gemini端点，卡片及表格均无对话入口，详情/复制保留。受控gpt-4o-vision-verifier输入text/image、输出text/audio保留聊天；vendor-image-verifier仅输出image无聊天。model-helpers.ts:38-88明确排除专用图片/视频/embedding/rerank端点、非text输出及已知生成模型家族；当前候选对应回归覆盖generic endpoint组合与多模态聊天。卡片和表格按钮顺序实测复制在前、对话在后；本轮Tab从复制进入有模型名的对话按钮。未发送模型请求。 |
| A4 | passed | brief.md | A4：模型预选等待有效选项加载；当前游乐场分组可用且支持目标模型时沿用，否则选账号可用且支持目标模型的最低倍率分组；空可用分组、目标不可用或加载失败明确提示，后续刷新不能静默改选另一模型冒充成功。 | 独立复核当前web/src/features/playground/hooks/use-playground-options.ts:106-225与对应Runtime回归收据：等待groups，先保留支持目标的当前组，再按倍率寻找支持目标的可用组；空组、未知模型、请求失败明确报错，target存在时禁用普通组/模型fallback。按授权复用上一轮原始Chrome客观记录d0670c5a10f24:a56/a60-a75及n136-n270：真实登录选中claude-haiku-5-5/ant cc20x；groups延迟3000ms、models延迟2000ms时保留支持目标的当前ant cc20x，虽另有更低组；空groups/503显示明确错误，恢复仅verify-other并刷新仍未静默改选。此路径本轮定向修复未改变，上述行为由本轮独立审查及当前候选测试覆盖共同确认，不继承Builder结论。 |
| A5 | passed | brief.md | A5：允许公开查看时访客使用公开分组倍率及相同筛选规则，入口提示“登录后体验”；登录后返回目标模型并刷新账号定价及权限。站点要求登录查看模型广场的设置保持有效。 | 独立核对当前use-pricing-data.ts按user.id与group隔离定价queryKey、pricing route保留requireAuth、auth-redirect.ts:46-79只允许同源http/https重定向。按授权复核上一轮原始Chrome记录：a47-a56复制后Tab/Enter进入sign-in?redirect=%2Fplayground%3Fmodel%3Dclaude-haiku-5-5，真实POST login n108/200后回原目标并预选；回模型广场产生新的账号pricing请求n116/200。认证及身份查询路径未因本轮A2/A3/A6定向修复改变；本轮真实已登录pricing请求n17及清理mock后n86均200，43模型目录恢复。未再次重复登录，也未改服务器登录可见性设置；该设置继续由现有route guard独立代码核对。 |
| A6 | passed | brief.md | A6：折扣在卡片中为醒目的独立标签，仅参考描边、小标题及较大折数的两级层次；配色使用当前主题语义色，亮色浅底、暗色低亮度底色，折数和强调色契合主题主色并保持对比度，随主题切换更新，不固定金色，列表采用相容的紧凑样式；七语准确表达同一优惠（中文3折等价于其他语言减价70%），支持语言切换、明暗主题、键盘和可访问名称；窄屏不遮挡名称、价格、详情、复制及对话入口。 | 本轮Chrome真实UI逐一切换en/zh/zh-TW/fr/ru/ja/vi，0.3中文3折与其余语言减价70%等价，.995在fr/ru/vi分别准确显示Jusqu’à -0,5%/Скидка до 0,5%/Giảm tới 0,5%；near1科学记数及1e-17真实支付比例七语均保留。未再出现parseFloat逗号误判。卡片两级标签和表格紧凑标签沿用语义primary；实际light与dark切换，边框primary/25→primary/30、背景primary/8→primary/12实测改变。390px截图无水平溢出（innerWidth390、clientWidth/scrollWidth380），名称/价格/详情/复制/对话可见，Tab从复制进入具可访问名称的对话按钮。截图marketplace-verifier-20-dark-390.png及本轮debug导出保存；键盘Enter登录流程另复核适用旧原始记录。 |

## 检查

| 检查 | 命令 | 工作目录 | 状态 | 退出码 | 耗时 |
| --- | --- | --- | --- | ---: | ---: |
| Vitest pricing and playground tests | run test src/features/pricing/__tests__/ src/features/playground/hooks/__tests__/ | web | passed | 0 | 21892 ms |
| TypeScript typecheck | run typecheck | web | passed | 0 | 6772 ms |
| Oxlint all change TypeScript files | x --no-install oxlint src/features/pricing/components/index.ts src/features/pricing/components/model-card.tsx src/features/pricing/components/pricing-columns.tsx src/features/pricing/components/pricing-table.tsx src/features/pricing/components/chat-entrance-button.tsx src/features/pricing/components/model-discount-badge.tsx src/features/pricing/hooks/use-pricing-data.ts src/features/pricing/lib/model-helpers.ts src/features/pricing/types.ts src/features/pricing/__tests__/model-chat-entrance.test.tsx src/features/pricing/__tests__/model-discount.test.ts src/features/playground/hooks/use-playground-options.ts src/features/playground/hooks/__tests__/use-playground-options.test.tsx src/features/playground/index.tsx src/features/playground/lib/options/playground-option-utils.ts src/routes/_authenticated/playground/index.tsx src/lib/format.ts src/i18n/static-keys.ts | web | passed | 0 | 1582 ms |
| Web production build | run build | web | passed | 0 | 18946 ms |

### Builder 报告的证据

以下为 Builder 报告，不等同于 Runtime 检查凭据或独立验收结果。

- Red regression before fix: passed — 新增真实组件及七语资源渲染用例在原实现观察到25 failed/13 passed，失败包含tiny/near-one/fr-ru-vi/payment及错误chat入口。先修正一处测试导入重名后得到真实业务失败。
- Focused pricing component and helper Vitest: passed — bun run test src/features/pricing/__tests__/model-chat-entrance.test.tsx src/features/pricing/__tests__/model-discount.test.ts；2 files/48 tests passed，exit0。
- TypeScript typecheck: passed — bun run typecheck (tsgo -b)，exit0。
- Touched-file oxlint: passed — bun x --no-install oxlint src/features/pricing/components/model-discount-badge.tsx src/features/pricing/lib/model-helpers.ts src/features/pricing/__tests__/model-chat-entrance.test.tsx；0 warnings/0 errors，3 files，exit0。
- i18n sync: passed — 14 translations via add-missing-keys.mjs then bun run i18n:sync；七语missingCount/extrasCount均0。临时脚本已删除。
- Chrome targeted acceptance: passed — Chrome155 instance2303ebf6，真实目录四个Seedance卡片/表格无chat，复制/详情保留；单个GET pricing隔离mock验证tiny/near-one/1e-17/.995/.3，法俄越卡片及越语列表语义正确，paid指定组去最低限定，390px无横向溢出。mock已停用，恢复真实43模型响应，语言恢复简中，session关闭。
- git diff --check: passed — exit0，无空白错误。
- Formal full test/build plan: not-run — 遵授权避免开发期重复完整正式计划；已在verificationChecks保留test/typecheck/build并加入lint，由Runtime执行。
- 已知限制: 缺少模型元数据时保留必要家族fallback；不声称可推断任意重命名别名的实际上游能力。未改后端路由/计费/目录契约。
- 已知限制: 本轮不重复Action17已通过的真实登录/明暗/键盘完整验收；由新的独立Verifier裁定当前完整候选。
- 已知限制: 浏览器debug完整导出到Temp；请求及操作未丢弃，但console记录有1172条裁剪，不以缺失console证明无事件。未请求任何上游模型接口。
- 已知限制: i18n sync报告仍有既存ja/ru各5、zh4个untranslated项；本轮新增支付比例keys均已翻译，未顺带改无关key。
- 已知限制: 生产build及完整pricing/playground检查本轮尚待Runtime正式执行，不沿用旧候选的构建成功作为新候选结论。

## 阻塞项

_无。_

## 风险与跳过的工作

- 按用户要求复用当前candidate的Runtime四项检查：test-pricing-playground 21892ms、typecheck 6772ms、lint-marketplace-playground 1582ms、build-web 18946ms，均passed/exit0；inputBefore=inputAfter=cdb782476aae841f3faba85b19f4966ee9d4d3494f2f76f34ea88de865729d15，gate=e4c802456a294a4e1edc864a2990efc76838a1e2b5b33d579122234942812981。未重复运行整套检查。
- [Suggestion] model-helpers.ts:72-76对缺少完整模态的目录保留已知模型家族启发式，代码已明确ponytail限制；本轮真实Seedance/Seedream及明确endpoint/模态样本通过，未知未来家族不能仅凭当前样本证明。
- 本轮浏览器证据C:\Users\caiqy\AppData\Local\Temp\marketplace-verifier-20-browser-evidence.json，run d320efdcaa0cd，105 requests/30 operations，requests与operations零丢失；console有1641条淘汰，未据此声称完整console无错。仅一个GET pricing mock已removed，active_rules=0；清理后真实pricing200/43模型。debug stop/export均在session stop之前。
- 语言切换按现有UI自动PUT用户语言偏好，最终已切回简体中文；后台另有自动auth refresh。证据无模型调用或真实服务器配置写入。未修改产品/正式产物，未accept/archive/commit/push。A4/A5复用旧原始客观记录并明确适用性，没有把旧结论直接当本轮结论。

## 之前的迭代

| 目标周期 | 迭代 | 尝试 | 结果 | 未解决项 | 摘要 | 完成时间 |
| ---: | ---: | ---: | --- | --- | --- | --- |
| 1 | 1 | 1 | fail | A2, A3, A4, A5, A6 | 需整改。独立核对全部A1-A6：A1 passed；A2/A3/A4/A6 failed；A5 blocked。确切反例包括有效倍率舍入为0/10折、真实图片模型端点组合误显示对话入口、空可用分组不提示、未知目标失败后选项更新静默改选，以及日语折扣含义反转。应由Runtime回Build修复；缺失浏览器闭环/窄屏/语言切换及lint证据需后继候选补齐，未接受、归档、提交或推送。 | 2026-10-28T04:41:08.891Z |
| 1 | 2 | 0 | recovery | — | 用户要求更醒目的参考图折扣标签、交换对话和复制图标位置、选择分组后显示当前分组折扣，替换原筛选不改变折扣约定；保留已完成修复 | 2026-10-28T05:09:21.708Z |
| 2 | 1 | 1 | fail | A2, A3, A6 | 需整改：全部A1-A6独立裁定完成，A1/A4/A5通过，A2/A3/A6失败。三项确定阻断：极端正倍率显示0折/固定值失真；Seedance视频模型错误出现对话入口；fr/ru/vi正常0.5%优惠被parseFloat本地化逗号误读为0.0001%。未修改产品/正式产物，未accept/archive/commit/push；由Runtime回Build统一整改。 | 2026-10-28T06:36:02.002Z |
| 2 | 2 | 1 | pass | — | 通过：全部A1-A6由新独立Verifier逐项裁定。前轮A2极小/near1数值失真、A3真实Seedance误入口、A6法俄越逗号精度三类Blocker在当前候选均未复现，已补本轮Chrome业务证据并复用匹配候选的Runtime检查。浏览器mock已清理、真实目录恢复、证据导出并停止会话；验收结论不等于已accept/archive。 | 2026-10-28T07:12:48.151Z |



## 结论

通过：全部A1-A6由新独立Verifier逐项裁定。前轮A2极小/near1数值失真、A3真实Seedance误入口、A6法俄越逗号精度三类Blocker在当前候选均未复现，已补本轮Chrome业务证据并复用匹配候选的Runtime检查。浏览器mock已清理、真实目录恢复、证据导出并停止会话；验收结论不等于已accept/archive。
