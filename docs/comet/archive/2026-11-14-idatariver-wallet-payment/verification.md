---
generated_from_state_version: 42
---

# 验证

## 当前结果

- 结果: **已归档**
- 验证情况: **已完成检查，验证结果已确认**
- 目标周期: 2
- 迭代: 4
- 验证器尝试次数: 1
- 完成时间: 2026-11-14T02:14:58.261Z
- 摘要: Independent QA40 PASS all11 for d6a0626b5b2eb3498bdf77e005b0055fb3a79e8f1aba36005e441cdcf232b7bb. Only affected settings-tab A10 supplemented using Runtime39 and own isolated browser; reviewed unaffected evidence inherited from QA35/checks30/QA31/QA33. Browser left SPA, capture stopped active_rules0, own window closed. 45 source hashes unchanged. Full report D:/Temp/mowan-idr-verifier40-report.md.

## 验收

| 编号 | 结果 | 来源 | 验收项 | 原因 |
| --- | --- | --- | --- | --- |
| A1 | passed | brief.md | A1：钱包原入口展示允许销售的固定套餐和支付方式，选套餐不创建订单，点击继续才创建 quantity=1 的单SKU订单；测试SKU不可售，未配置/不可用有明确提示，其他支付方式行为保持兼容。 | QA35 report:19 + QA31 wallet confirmation and real SKU/quantity tests inherited after scoped impact review: settings trigger reorder does not touch selection/create. Full scope/source/limitations: D:/Temp/mowan-idr-verifier40-report.md; inherited detailed sources D:/Temp/mowan-idr-verifier35-report.md. |
| A2 | passed | brief.md | A2：确认框分别展示服务端快照的套餐售价USD、到账额度以及渠道返回的实付金额/币种；不重复应用通用折扣/汇率，未知费用或空值不显示为已确定实付。 | QA35 report:20 + QA31 USD quote/component and QA33 BTC/CNY/null proof inherited; no quote/snapshot/currency changes. Full scope/source/limitations: D:/Temp/mowan-idr-verifier40-report.md; inherited detailed sources D:/Temp/mowan-idr-verifier35-report.md. |
| A3 | passed | brief.md | A3：确认付款后保存原订单标识并同页前往经校验的官方支付URL；回跳恢复对应订单；无链接、非法URL和获取链接失败保留订单并提示重试，不创建替代订单。 | QA35 report:21 + QA31 original-order persistence, explicit same-page navigation and URL/error tests inherited; no navigation implementation change. Real cashier not tested. Full scope/source/limitations: D:/Temp/mowan-idr-verifier40-report.md; inherited detailed sources D:/Temp/mowan-idr-verifier35-report.md. |
| A4 | passed | brief.md | A4：回站、刷新、重新进入钱包和重新聚焦时自动查询当前账户的待确认订单；查询有限重试，成功后刷新余额和历史记录；退出/切换账户不展示或查询前一账户订单。 | QA35 report:22,31-37 original native visible->check DONE->self100to112->same-open-history success and real Hook/Wallet tests inherited; no recovery/account/listener changes. Rare exact pending OS ordering remains limitation, not extra criterion. Full scope/source/limitations: D:/Temp/mowan-idr-verifier40-report.md; inherited detailed sources D:/Temp/mowan-idr-verifier35-report.md. |
| A5 | passed | brief.md | A5：查询错误/超时与正常待支付状态分开显示；保留原订单及上次已确认状态，自动轮询结束提供手动查询，不能将上述事件直接标为失败、未付款或过期。 | QA35 report:23 + QA31 trusted-state and QA33 seven-language error/manual/link recovery inherited; no error-state changes. Full scope/source/limitations: D:/Temp/mowan-idr-verifier40-report.md; inherited detailed sources D:/Temp/mowan-idr-verifier35-report.md. |
| A6 | passed | brief.md | A6：历史订单可恢复原订单；继续付款前服务端查验用户归属、NEW状态和渠道可用性，再取得支付链接；DONE/REFUND/EXPIRED不可继续付，原链接失效不自动再建单。 | QA35 report:24 + QA31 original order ownership, NEW/method/terminal protection and QA33 recovered quotes inherited; no history/continue changes. Full scope/source/limitations: D:/Temp/mowan-idr-verifier40-report.md; inherited detailed sources D:/Temp/mowan-idr-verifier35-report.md. |
| A7 | passed | brief.md | A7：仅上游确认EXPIRED才展示过期并允许用户主动重新下单；尚无退款处理记录时，迟到DONE经完整核验可一次入账。REFUND在未入账时阻止后续入账；已入账时余额足够一次全额扣回，不足则不部分扣回、不透支并转manual_required；重复REFUND及任何已处理退款后迟到DONE均不再改变余额，manual_required不自动重扣；数据库失败整体回滚可重试。仅接收平台退款，不新增用户发起退款入口。 | QA35 report:25 + own QA31 actual SQLite/MySQL/PostgreSQL settlement/refund/live-cache tests inherited; no Go/DB/ledger changes. Full scope/source/limitations: D:/Temp/mowan-idr-verifier40-report.md; inherited detailed sources D:/Temp/mowan-idr-verifier35-report.md. |
| A8 | passed | brief.md | A8：重复点击、重试和并发请求不重复创建同一支付尝试；已取得订单号的所有失败均保留原订单；创建超时结果未知时保留待核实尝试，通过服务端可信关联恢复唯一匹配订单，无法确认时保持待核实且不自动再下单，不承诺平台不存在的幂等能力。 | QA35 report:26 + QA31 ambiguous/unique recovery and real concurrent idempotency plus QA33 seven identical request pairs inherited; no attempt/persistence/API changes. Full scope/source/limitations: D:/Temp/mowan-idr-verifier40-report.md; inherited detailed sources D:/Temp/mowan-idr-verifier35-report.md. |
| A9 | passed | brief.md | A9：伪造回跳/通知、错误订单归属、商户/项目/SKU/数量/金额/币种不匹配、无效渠道均不能触发错误入账；合法成功并发通知/查单仅入账一次，秘密和可用匿名查询凭据不泄露至前端、日志或错误正文。 | QA35 report:27 + QA31 merchant/project/SKU/quantity/money/currency/ownership trust-boundary and once-only settlement evidence inherited; no backend/auth/secret changes. Full scope/source/limitations: D:/Temp/mowan-idr-verifier40-report.md; inherited detailed sources D:/Temp/mowan-idr-verifier35-report.md. |
| A10 | passed | brief.md | A10：设置页可管理合法配置且密钥脱敏；禁用/错误配置安全失败；三数据库新建/升级/重复启动保持订单与余额完整；七语言、键盘操作、桌面和移动端状态完整，已有钱包支付及订单列表回归通过。 | Current candidate Runtime39 payment-settings-tabs passed exit0/6140ms plus own QA40 browser: General default, exact seven-tab order with iDataRiver last, actual panel click, targeted arrows/Home/End/Enter returning General and iDataRiver; 6/6 API requests mock-applied, option writes0. QA35 prior config/3DB/seven-language/wallet-keyboard/mobile/legacy evidence inherited as unaffected. Evidence D:/Temp/mowan-idr-verifier40-final-browser-capture.json and report.md. Full scope/source/limitations: D:/Temp/mowan-idr-verifier40-report.md; inherited detailed sources D:/Temp/mowan-idr-verifier35-report.md. |
| A11 | passed | brief.md | A11：平台CNY时，套餐卡片、原确认框及iDataRiver历史金额保留USD售价/到账额度，按有效平台显示汇率补人民币参考值，注明仅供参考、实际付款以渠道为准；USD及TOKENS/CUSTOM不附加人民币。来源缺失、非有限/非正/不可解析汇率或非有限乘积时保留USD并提示参考值暂不可用且不阻塞支付；正小额不得显示为0或把最小阈值冒充精确值。币种、汇率及语言切换同步更新；历史恢复按原USD快照及当前显示汇率；渠道实付的原币种、有效精度及未知提示保持，请求、SKU、quantity、订单价格、quota及结算/退款不受参考值影响。 | QA35 report:29 + QA31 reference/rate/status and QA33 CNY7.25/null and independent channel currency evidence inherited; no formatting/status/pricing/settlement changes. Full scope/source/limitations: D:/Temp/mowan-idr-verifier40-report.md; inherited detailed sources D:/Temp/mowan-idr-verifier35-report.md. |

## 检查

| 检查 | 命令 | 工作目录 | 状态 | 退出码 | 耗时 |
| --- | --- | --- | --- | ---: | ---: |
| Affected payment settings tab order and activation | run test src/features/system-settings/integrations/__tests__/payment-tabs.test.tsx | web | passed | 0 | 6140 ms |

### Builder 报告的证据

以下为 Builder 报告，不等同于 Runtime 检查凭据或独立验收结果。

- Actual settings tab regression: passed — D:/Temp/mowan-idr-tabs38-red.log initial1failed ordering expectedGeneral gotiDataRiver; final D:/Temp/mowan-idr-tabs38-green.log1passed5.31s aftertestextendedEnd/Enter
- Affected typecheck/lint/format: passed — D:/Temp/mowan-idr-tabs38-types.log1442files2projects noerrors;tabs38-lint.log2files0warnings0errors;oxfmt check2files passed;gitdiffcheck0
- Existing payment/funds evidence: not-run — User says do not repeat passed checks; inherited QA35/checks30 valid unaffected evidence, no114/3DB/router/build reruns
- 已知限制: Only moved one existingtrigger;new test file. NoSpec/Go/payment/locale/DB writes;devbasedsameRun/worktree, noGitdelivery/production/realorders.
- 已知限制: QA35PASSbelongsoldcandidate1eb301d9;newcandidate requiresindependent A10 limitedcheck and inheritancevalidityreview.
- 已知限制: FullrepoGo/frontendtest/lint historical failures andrealcashierunmeasuredlimits fromQA35 retained.
- 已知限制: Native currentcandidate Runtimecheckplan only payment-tabs.test.tsx; noold fourchecks copied, nofunds/router/wallet reruns.

## 阻塞项

_无。_

## 风险与跳过的工作

- QA35/checks30 certify prior candidate, not direct current-candidate checks; evidence reused only after documented impact review. Current Runtime39 executes only settings-tab regression. No broad tests rerun.
- Full-repository Go/frontend/lint historical failures remain; no real cashier/payment/production verification; local DB and native OS limitations remain as QA35.
- Browser used local synthetic auth/options/all-method deny fallback; screenshot timed out and no screenshot claimed. Targeted keyboard events are not claimed as uninterrupted full-keyboard traversal. Source mtime scope is corroboration, not old/new candidate byte snapshot.
- QA31/QA33 historic failures/blockers and capture limitations remain preserved; no acceptance/archive/Git/deployment performed.

## 之前的迭代

| 目标周期 | 迭代 | 尝试 | 结果 | 未解决项 | 摘要 | 完成时间 |
| ---: | ---: | ---: | --- | --- | --- | --- |
| 1 | 1 | 1 | fail | A1, A2, A3, A4, A6, A10 | Independent Verify FAIL for candidate 4a6443ac14cf6a0bc39089eb0f1850430f7fbd698f42fc4f27f25cdcaa1932f7: A1/A2 failed, A5/A7/A8/A9 passed within controlled test scope, A3/A4/A6/A10 blocked by missing complete browser evidence. Two external reproduction checks passed (meaning defects reproduced), bun run test --config D:/Temp/mowan-idr-qa-vitest.config.mjs exit0; git diff --check exit0. Matching Runtime checks reused with candidate/input identity intact. Return to Build before implementation edits. Keep confirmed Shape and dev-based worktree; no commit/push/merge/deployment authorized by this verdict. | 2026-11-13T08:49:31.678Z |
| 1 | 2 | 1 | fail | A2, A4, A6, A10 | Independent round2 FAIL. Original recovery trap and tiny rounding defects repaired and independently checked; A2 still fails because known channel actual amount/currency is not presented before immediate navigation. A1/A3/A5/A7/A8/A9 passed within stated controlled scope; A4/A6/A10 blocked by missing full browser journeys. New verifier action15 independently claimed and all ten judged. Evidence: own35 tests exit0; current Runtime4 passes with matching complete candidate fingerprint; mobile/return screenshots and browser capture. Return Build before edits and preserve current dev worktree/Shape; next candidate needs a new independent verifier execution. | 2026-11-13T09:31:16.377Z |
| 1 | 3 | 1 | blocked | A4, A6, A10 | Third candidate original A1-A10 independently assessed: A1/A2/A3/A5/A7/A8/A9 passed within stated controlled scope; A4/A6/A10 blocked by unfinished browser evidence. Formal result blocked, not pass or implementation fail. Source/Shape not edited. Browser evidence D:/Temp/mowan-idr-qa-round3-browser.json; amount screenshots D:/Temp/mowan-idr-qa-round3-known-amount.png and D:/Temp/mowan-idr-qa-round3-mobile-known.png. Runtime receipts D:/Temp/mowan-idr-verifier-18-receipt.json. User-approved CNY extension excluded from original verdict; original claim settled before requirements revision. | 2026-11-13T10:12:17.879Z |
| 1 | 3 | 1 | recovery | — | 用户在群内接受CNY人民币参考展示：保留USD售价和到账额度，按平台显示汇率补参考值，渠道实付保持返回币种，不改变订单和结算；保留原恢复/退款及先读报价后主动跳转规则。扩展A11，更新完整Shape重新确认；旧候选A4/A6/A10浏览器证据blocked如实保留，新范围需新候选独立全验。 | 2026-11-13T10:13:16.878Z |
| 2 | 1 | 0 | recovery | — | Runtime checks failed: idr-real-db. Preserve their receipts and submit a new Builder candidate after repair. | 2026-11-13T14:04:27.325Z |
| 2 | 2 | 1 | fail | A4, A10 | Original :28 independently judged allA1-A11 for candidate784605ad7b42c4085ff940c0b2ac14e6127af33b277f537029c5b7e3fa9ebae8. FAIL: A4 real native focus(hidden) before visibilitychange(visible) misses recovery check with pending original retained and polling stopped. A6 added actual EXPIRED/REFUND/no-channel checks and passed. A10 blocked remaining full valid-quote keyboard/exception-language browser matrix. A1/A2/A3/A5/A6/A7/A8/A9/A11 passed controlled scope. Evidence distinguishes actual observations, unit/Runtime checks and PM reports. No source/Shape edits; cleanup complete, DBs retained. Return to legal Builder for minimal recovery-event fix; no accept-result, Git or production action. | 2026-11-13T15:29:44.509Z |
| 2 | 3 | 1 | blocked | A4, A10 | QA31 independently reviewed all A1-A11:9passed, A4/A10blocked; no implementation edits/real payments/production/Git delivery.114frontend tests and actual3DB funds tests pass. Full evidence/limitations: D:/Temp/mowan-idr-verifier31-report.md; capture D:/Temp/mowan-idr-verifier31-complete-capture.json. Builder evidence is distinguished from personal checks. Existing QA28FAIL/checks25 failure preserved. | 2026-11-13T16:19:32.587Z |
| 2 | 3 | 1 | recovery | — | 用户在群中明确选择继续补验证，并要求已通过项不要反复验证。当前候选1eb301d954e53ad01fc72215cd05d85cc35743d433ba0d03e71ab36601e900eb保持冻结，沿用QA31九项独立通过及仍有效的Runtime检查，不重跑114前端测试、真实三库资金/退款、类型/路由/构建。只补A4待恢复原订单回焦取证及A10真实语言选择切换、异常恢复、连续完整键盘、窄屏/旧支付尚缺证据；产品只审阅既有与新增证据及真正缺口。独立Verifier完整回报A1-A11时标明继承和新增来源。仅实现/输入变化、证据失效或明确新影响才解释并重验相关范围；本次不修改实现或验收标准，不接受结果、不Git交付、不生产。 | 2026-11-13T16:28:54.922Z |
| 2 | 3 | 2 | blocked | A4 | QA33仅补缺口：10项passed，A4 blocked；九项QA31通过及checks30沿用，A10新增七语言异常/同ID重试/连续键盘/390px/legacy和汇率集成证据。报告D:/Temp/mowan-idr-verifier33-report.md。三个浏览器窗口全关闭，精确关联清除、语言en、仿真已恢复，最后抓取active_rules=0。无实现写入、无新候选、未重跑通过检查。 | 2026-11-13T17:52:58.396Z |
| 2 | 3 | 2 | recovery | — | 用户已明确确认按现有证据恢复正式复核，不重测、不改代码。纠正将旧缺陷的精确原生OS事件顺序诊断路径额外作为验收门槛的判断；保持原A1-A11及同一冻结候选1eb301d954e53ad01fc72215cd05d85cc35743d433ba0d03e71ab36601e900eb。新独立Verifier应核对并复用有效Runtime检查、QA31和QA33证据，A4结合两种顺序的业务Hook红绿回归与真实浏览器自动查单及余额/已开历史刷新记录重新判定。未再捕获特定原生顺序如实记为验证边界，不虚称已原生实测。其他通过项不重复执行；不改变规格、不接受最终结果、不Git提交推送合并或生产发布。 | 2026-11-14T01:03:53.614Z |
| 2 | 3 | 3 | pass | — | 独立Verifier35按用户确认以原A1-A11复核同候选现有证据，11项passed。A4基于原生n43原单DONE→n44余额100→112→n45已开history Success及生产Hook两序/账户/单飞回归，纠正额外OS轨迹门槛并保留未原生再捕获边界；A10采用QA33完整补证。checks30与QA31/33明确继承，29原结果哈希一致，本轮未开浏览器或重跑通过项。全仓测试/lint历史失败及真实支付未测保留。详见D:/Temp/mowan-idr-verifier35-report.md。 | 2026-11-14T01:13:32.540Z |
| 2 | 3 | 3 | recovery | — | The user rejected the Skill-coordinated Verifier pass. | 2026-11-14T01:48:28.605Z |
| 2 | 4 | 1 | pass | — | Independent QA40 PASS all11 for d6a0626b5b2eb3498bdf77e005b0055fb3a79e8f1aba36005e441cdcf232b7bb. Only affected settings-tab A10 supplemented using Runtime39 and own isolated browser; reviewed unaffected evidence inherited from QA35/checks30/QA31/QA33. Browser left SPA, capture stopped active_rules0, own window closed. 45 source hashes unchanged. Full report D:/Temp/mowan-idr-verifier40-report.md. | 2026-11-14T02:14:58.261Z |



## 结论

Independent QA40 PASS all11 for d6a0626b5b2eb3498bdf77e005b0055fb3a79e8f1aba36005e441cdcf232b7bb. Only affected settings-tab A10 supplemented using Runtime39 and own isolated browser; reviewed unaffected evidence inherited from QA35/checks30/QA31/QA33. Browser left SPA, capture stopped active_rules0, own window closed. 45 source hashes unchanged. Full report D:/Temp/mowan-idr-verifier40-report.md.
