# 智能路由完整目标规格

版本：smart-routing-v1（魔丸自定义算法，本轮用户已批准完整Shape及粘性补项后执行）。来源覆盖与分类见brief的Source coverage。本规格描述完整交付行为；参考方后台、真实流量和权重均未公开，不宣称已还原。

## 1. 入口、默认和兼容

创建/编辑Key在原分组控件前放“路由模式”：智能路由、固定分组。新Key默认智能路由→综合最优。智能下有综合、价格、速度、成功率、自定义五项；固定下保留现有单选。选择智能不修改账户所属组。

旧Key无扩展配置行时为legacy：group为空继续继承用户组；固定group继续固定；group=auto保留auto_groups缺省继承、显式空列表、有序显式列表、cross_group_retry原值。编辑旧Auto时专门显示只属于该旧Key的“旧版Auto（保持原行为）”兼容状态，不能伪装成单选固定组或新综合。编辑无关字段不产生扩展行、不截断/过滤原始Auto字段、不替换失效/空group；失效值只标识，运行时仍按既有权限拒绝。显式更改候选时才校验并写新值。

新建UI显式提交routing=smart/balanced；API创建在routing与legacy路由字段全未提供时采用同样新默认，明确提供group（含空字符串）/auto_groups/cross_group_retry而没有routing时保旧客户端显式意图。不能同时提交互相冲突的新旧路由值。更新缺routing时保持现状；未修改路由的UI省略路由三元组，服务端按字段是否提供更新，不能以Go零值推断缺省。model_limits_enabled等硬限制也不因路由表单重建而被重算或清空。

创建/更新严格区分缺省、null和对象；null只允许更新时显式恢复该Key原legacy快照，且仅控制台有权执行，self拒绝null；没有legacy快照的新Key不能凭空恢复。恢复时保留mode=legacy的扩展行并递增version，不删除行重置为0，避免CAS的ABA。切换模式保本次编辑草稿，提交仅写当前模式规范化字段，取消放弃草稿。原legacy快照仅用于显式恢复，永不参与新模式选路。

本轮范围按2026-11-17用户决定限定为目标功能及本次引入的问题；非本次任务引入的既有缺陷暂不处理。旧固定/Auto Key通用Task在上游受理后本地保存失败等既有异常资金行为不纳入耐久提交扩围，不追溯补建历史Task或余额delta。不得以兼容为由保留本次新增回归，也不得把候选实现中本次新增的行为当成基线既有行为。共享同步DB余额权威等本轮必要公共接点仍按§7实施，不等于升级旧Key全部资金生命周期合同。

2026-11-17用户进一步明确本项目今后验收不做压测，已证实非本次开发引入的限制可保留；随后明确同意将本次新增采集器造成且无法补回的历史终态缺证另列已知限制，按§7.2仅解除这项历史缺证阻塞后继续独立验收。必要功能、当前实现正确性、安全及独立验收保持；历史失败和收费未知不改写，不将未知产品问题认定既有。本轮只读既有材料并修订正式合同，不新增测试、不改生产，候选先交组长复核，暂不启动Verifier。

### Scenario: 新Key入口与默认
Acceptance: A1
Given 创建新Key，When 未改路由，Then 保存智能综合；五智能策略可选，固定只允许单选组。

### Scenario: 旧Key无损往返
Acceptance: A2
Given 旧固定、空组或Auto各种缺省/空/有序/失效值，When 只改名称并保存，Then 路由序列化和运行语义均不改变，不生成新综合策略。

## 2. 配置、候选和权限

候选为(client_model, business_group)，物理Channel由现有渠道机制选取。请求模型保持不变；自定义按请求模型查该模型的组，不把多个已配置模型当成替换请求模型的池。硬权限集合=当前有效账户/Key/IP/模型权限∩当前可选业务组∩该模型启用能力∩请求endpoint/plugin类型/pin约束。group=all的已有能力展开为当前真实获权组，不将all/auto当新策略业务候选。模型别名沿用既有解析，按客户端模型标识保存策略，不自行把两个别名合并扩权。明确保存smart或新fixed的扩展配置成为该Key路由授权范围；保留的legacy group/auto字段仅作快照，不能反过来扩权或提前误拒绝新模式，TokenAuth需按当前已保存模式解释分组；账户/模型/IP/额度等硬限制始终有效。

动态策略从全部当前获权候选剔除excluded_models、excluded_groups与excluded_model_groups；新增获权组自动参与，界面明确提示。自定义models映射严格白名单，每模型包含groups数组、strategy=balanced|price|speed|success|manual、try_soft_offline默认false。manual数组即业务组优先顺序；动态时groups是集合，保存使用稳定规范序。未配模型403 routing_model_not_configured；空候选503 routing_no_candidate，不继承全局Auto或默认组。自定义至少1模型、每模型至少1组，不允许重复、空标识、递归custom或未知枚举。被撤权已存值保留展示为失效，不可被当有效候选；显式编辑该策略必须修复失效项再保存。

信任边界限制：策略JSON≤64KiB，模型≤200，每模型组≤100，排除模型≤200，排除组≤100，模型/组标识长度沿用已有模型/分组约束；超过限制返回400，不截断。GET候选分页默认50、最大100，搜索≤128字符；既有数据超新限制可只读无损保留，无关名称更新不重校验旧值。

候选目录与报价按当前身份过滤，不输出物理Channel ID、上游地址或凭据。每次新发送前重新读当前Key路由version、Key/账户状态与权限，版本变化则在剩余预算内重建候选并保留已尝试集合；已开始输出/已受理任务不切换。发送前安全复核不采用可能陈旧的Token Redis或用户缓存当权威；与更新/禁用/删除共用DB锁或版本检查，使提交资格在发送前最后一次检查时线性化，检查后已在途请求按既有协议结束，不承诺能撤回已发送字节。持续改配置仍受有限候选扫描及原deadline/attempt上限，不获得新预算。

### Scenario: 动态获权与排除
Acceptance: A3
Given 新获权/撤权和模型/组排除，When 构造下一次尝试，Then 仅剩当前合法且未排除候选，集合为空直接失败。

### Scenario: 自定义严格校验
Acceptance: A4
Given 两步配置或API保存，When 未选模型、模型无组、重复/递归或请求未配模型，Then 保存/调用明确拒绝，正常往返保留草稿。

## 3. 统一报价

报价解析复用现有计费表达式、旧倍率/按次、任务provider覆盖与单位。最终生效组倍率先用userGroup→usingGroup特殊倍率，再普通组倍率，显式0保留。输入/输出/缓存创建/读取/音图/按次/秒/credit按实际计费定义展示，七语言格式化复用现有工具。不另建价格表，不把前端显示汇率/充值倍率用于选路成本。

比较优先级：同一模型、计费表达式/计价单位/插件provider语义完全相同且仅组倍率不同，直接按有效组倍率比较；表达式含跨条件或provider差异时，只有完整请求的同一计价用量/参数可由现有计费引擎可信估算，才按USD estimated_total比较。未知输出使用明确标记的估计向量：输入为既有token估算，输出为经校验请求上限；未提供上限时取该模型配置上限与1024中的较小值，模型无上限则1024；缓存未知按未命中，图/音/视频等取已校验请求数量/时长及现有任务事实。此估计仅用于排序/展示，不改变既有预扣输入口径与实际结算。

需要上游结果/未知usage facts、不能安全估计的表达式或混合计价返回comparison=unavailable、原因和各自价格规则；不能把输入价与按次价数字直接比较。可比候选先按估价排序，不可比候选居后按稳定ID排列；全部不可比时价格策略稳定ID降级并提示“价格不可比较，按固定顺序尝试”。综合的价格维对不可比统一0.5，不能冒称低价。业务组内多个provider价不一致时展示各生效变体，组报价为范围且不可比较，除非本次请求硬约束唯一provider并能算出完整估价；不挑最低子价冒充组价。

### Scenario: 用户生效价与不可比
Acceptance: A6
Given 特殊倍率、零价、表达式或异计价provider，When 查询候选/估价，Then 返回真实单位与可比状态，预估不作为最终账单。

## 4. 选路计划与硬约束

请求生命周期只有一个新策略RoutePlan：策略版本、当前合法组、已尝试物理Channel集合、必要的origin换key指纹集合、attempt序号、全局remaining、请求deadline。智能预算=max(1,1+max(0,common.RetryTimes))次实际上游拨号/提交，换key也占预算；pin/strict/endpoint可进一步缩小，不能增大。deadline沿用已配置请求/协议超时并取客户端deadline较早值；取消即终止选下一候选，但不取消已受理任务的耐久收尾。探查不存在渠道不算发送，但一次选路只扫描当前候选×现有渠道有限集合，耗尽返回exhausted，不循环钳最后priority。内存model/channel_cache.go与DB model/ability.go都执行相同有限排除/耗尽语义；各自原weight算法保留，不宣称两路径已同分布。

每次先决定候选组，再按该组原priority降序、同档weight抽取未尝试且满足现有filters的物理Channel；该Channel成功取得发送资格后记入全局已尝试集合。普通相同物理Channel即使出现在多个组也不重发，避免仅换收费组重复同一上游；唯一例外为现有origin pin明确允许同Channel换key，此时按(Channel,key的服务端指纹)去重、仍锁原组、每发送计一次预算，绝不记录原key。管理员single-attempt pin无此例外。组内仍有合法渠道时保持该组重试优先；耗尽才取策略下一组，均受同一全局预算，不能因为换组重置。manual严格按已存顺序；其余策略在合法未尝试剩余组上重算。插件预选与Distribute共享同一CandidateID/Group/Generation及选中Channel，未发送预选不推进预算；首选普通relay或插件后不跨执行器类型，插件decode和请求能力校验逐候选执行，不能仅靠FilterRequestPath证明多模态兼容。

单组自定义/固定模式不跨组，但组内仍可能重试多个渠道；界面称“固定分组/只在该分组尝试”，不称锁定一个物理渠道。旧Key继续既有retry语义，新去重/预算不偷偷改旧Auto。

pin/strict/origin优先级沿用现有机制，但最终物理Channel和业务组还必须属于该Key策略候选。管理员调试pin不扩大普通Key权限。strict绑定失效继续返回strict_session_binding_unavailable；不能退到综合。origin任务查询/变体/续作保原任务Channel，且查询鉴权/任务归属沿既有契约；新创建任务才用新策略，不把历史任务随配置变化迁往别组。

软健康只跳过临时健康下线候选。try_soft_offline仅自定义每模型所选组有效，为true时按既有策略包含其软下线组；不是越过禁用Channel。全部软下线且未选仍尝试时返回503 routing_no_healthy_candidate。正常/拥挤候选可正常参与，manual前组拥挤时可先选后续非拥挤组；全部拥挤仍按原顺序，不无限等待。软下线/正常信号不能改变管理员禁用/撤权状态。

### 4.1 粘性会话与智能优先级

复用service/channel_affinity.go、request_policy.go及relay_error.go，不建立第二套会话框架。先满足当前Key合法候选/权限、资金及限流、协议安全、pin/origin/已建连接等硬约束，再处理有效粘性绑定，最后才智能评分或manual顺序。管理员pin不得扩Key权限，与strict/origin冲突按既有明确约束报错，不借智能策略消解。所有实际粘性发送都计统一attempt预算和已尝试集合，报价/预留/结算使用命中绑定的真实业务组。

- off：不读写智能粘性绑定；每个新请求按当前已保存智能策略选路，已建上游连接或origin等硬绑定仍有效。
- prefer：首次未绑定或TTL到期由智能策略选择；绑定的原业务组+原Channel仍在当前候选、能力/filters合法且健康允许时优先使用，不因其他组更便宜、更快、评分更高或manual排在前面而跳走，单纯congested不打断有效绑定。原绑定撤权、排除、禁用、配置变更使其失效或被soft_offline避让时，才在当前合法集合重新选路；是否清理旧失效缓存沿用既有KeepOnChannelDisabled配置。真实发送失败也只有原协议明确可安全重试、未提交输出/受理、预算仍有剩余才重新选路，保留全局已尝试去重。不得用选择失败伪造一次发送或新增预算。
- strict：TTL有效且已有绑定时只允许原业务组+Channel。若不在当前合法候选、不可用、硬门禁/健康拒绝或调用失败，明确终止，不换组或Channel；保留strict_session_binding_unavailable错误契约。软健康需拒绝时保原绑定并拒绝，不能偷换prefer或借半开探测迁移；自定义try_soft_offline只可允许原绑定合法Channel的受控尝试。首次从未绑定或TTL已到期视无当前绑定，可以智能选择，不能把“查到绑定但失效”误报为未绑定。

新智能绑定在既有affinity缓存机制中采用隔离的版本化namespace，值包含实际business_group及ChannelID，保留规则匹配键、模型/身份边界和既有TTL；旧legacy的namespace和值保持原样，不把旧Channel-only命中猜成当前排序第一个收费组。多个业务组共用同Channel时按绑定组解释并重新核验，绑定组被排除后prefer可重新选但strict不得转用同Channel的另一组收费。成功后按既有SwitchOnSuccess/TTL与真实成功规则记录/刷新绑定：SwitchOnSuccess开启时失败/取消不覆盖，关闭时沿既有配置语义，不强改成仅成功更新；输出或accepted/unknown边界不因允许写缓存而获得重试资格。成功切换后记录真实最终组+Channel。规则、Key策略或权限变化在下一次发送前复核；不承诺永不失效、必命中上游缓存或跨provider保留会话状态。

### Scenario: 单组和手动顺序
Acceptance: A7
Given manual/四动态及合法prefer绑定，When 其他组排序更优或仅绑定组拥挤，Then prefer原组+Channel优先；off重新按策略选。无有效绑定且前组失败/拥挤时仅在清单内回退，组内priority/weight仍有效，单组不跨组。

### Scenario: 所有入口与全局预算
Acceptance: A8
Given HTTP初选/重试、共享/原生任务、Realtime、Responses WS与MJ入口及多组共用Channel的粘性命中，When 运行智能策略，Then 复用RoutePlan和明确去重例外，粘性也占总预算，按绑定真实组计费，deadline不被局部计数覆盖。

### Scenario: 软健康覆盖不越权
Acceptance: A11
Given prefer/strict命中软下线绑定及自定义try_soft_offline开关，When 下一发送，Then prefer仅在允许安全重选时回退；strict未获原Channel尝试资格则保绑定拒绝，不切组；管理员禁用/无权限/限流始终硬拒绝。

## 5. 指标与算法 smart-routing-v1

### 5.1 真实观测与分口径

新attempt观测在实际发送前start，结束后finish exactly-once；键含request关联ID/attempt ID、client_model、business_group、ChannelID、protocol_class和stage。硬拒绝/资金不足/客户端参数错不算供应商失败；实际网络连接失败、明确可归属上游的429/5xx、协议错误和上游超时算失败；客户端取消单列ignored，真实已发生上游失败仍计失败。stream首token记录TTFT但成功率必须终态成功；同步为该attempt开始到完整响应；异步提交为提交延迟/明确受理或失败，任务完成为独立task_terminal口径，不能把受理当任务成功。立即完成任务计终态一次，CAS终态获胜者记录，提交失败不得消失。

维度固定为K=(client_model,business_group,protocol_class,provider_key,operation_key)，stage另列attempt|task_terminal，不藏在protocol_class里。protocol_class枚举sync、stream、async、responses_ws、realtime；provider_key为服务端解析的内置adaptor类型+兼容版本，或插件key+generation；operation_key为服务端规范化端点/任务动作（例如chat、responses、image.generate、video.create、MJ imagine/variation），不能用客户端任意文本制造新维度。attempt事件还含ChannelID；完整分钟桶唯一键为(node_boot_id,K,stage,ChannelID,minute)，lease记录相同K/stage/ChannelID。任务保存创建时完整K，不用后改插件generation重标历史。

折叠只允许在同一K/stage内汇总当前硬合法、同provider/operation的Channel及node；撤权/禁用渠道的历史不参与当前候选排名。异步R/S只取task_terminal，async提交成功率/延迟另展示且提交失败仍参与即时健康；其他协议R/S取attempt。实时健康与半开用(K,stage)，异步提交与终态的breaker分别保留，任一软下线即跳过对应可发送variant；active提交与长任务分别计，不双计同一工作。健康短窗/429/分摊所用active必须来自同一variant的K及相应stage，不能用全站/其他操作统计。

候选仍是(model,group)，不是provider新候选：若请求约束只能落一个K，直接用该K的R/S/L；若同组可能由多个provider/operation variant执行，不混合其延迟/成功率给一个伪精确分数，该组R/S标mixed_provider_operation未知并走既定降级。L可按该组所有当前可执行variant的active求和、429按同一stage的已发送数加权计率；这是公开的组观测负载折叠，不是速度混样本。仅部分variant软下线时，组内抽取跳过那些variant；全部软下线组才显示软下线，部分下线且余项有健康数据则显示降级，缺数据为unknown；normal/congested/unknown详细状态按各variant展示，健康批选仅加入所有当前可执行variant均normal的组。组级半开只能由一个具体(K,stage)claim放行，不借其claim放行其他variant。

排名窗口滚动24h，卡片主响应近1h；24h成功率原始success/n与样本数一起显示。响应按protocol_class：stream用成功attempt TTFT；sync用成功attempt总响应；async用成功任务从submit到finish的总耗时（包含供应商排队），另展示提交延迟。终态任务继承创建时模型/组/Channel/protocol，不用查询请求的当前策略。不同protocol、任务操作或provider语义不混用速度样本；每类缺数据独立降级。原pkg/perf_metrics整请求统计不回填新attempt。

24h至少20个eligible终态样本才认为成功率可信；速度至少5个成功速度样本。最近成功/失败观测距今>10min标stale，不再用于主排序指标；滚动24h的原值仍可显示但标“过期，不参与评分”。1h响应缺样本显示“—/样本不足”，不显示0；缺成功率不显示0%或100%。失败观测仍可驱动短时避让，不能因样本不足无视连续失败。

### 5.2 归一化与四动态

在同一硬合法、健康可尝试集合内计算：

- 价格P：可比成本cost≥0，用1-(cost-min)/(max-min)；全等=1，单个可比=1，不可比=0.5。不得NaN、溢出或用舍入后的显示价比较。
- 可靠性R：(success+19)/(n+20)（Beta(19,1)先验），主排序只有n≥20且新鲜者使用；未知/过期在综合中用0.5且明确缺样本，不把先验当用户可见真实95%。
- 速度S：成功样本算平均ms，按1-(mean-min)/(max-min)，全等=1；未知=0.5。长任务与对话独立集合，不按失败快速返回速度奖励故障。
- 实时空闲L：1/(1+active)*(1-rate429_60s)，active来自有效在途租约，rate429_60s为该口径近60s上游429/已发送数；无60s发送时rate=0。此值只代表本网关观察负载，不冒称知道上游真实队列/容量。租约/共享信号不可用时L=0.5，显示“负载未知”。
- 综合score=0.20P+0.40R+0.25S+0.15L。公开权重、窗口和缺样本处理，版本随行为变化升级。

price先按comparison可用性，再cost升序，cost相等按R降序/S降序/group ID稳定字典序；success先将可信样本置前，按R降序，后price规则，最后stable ID；speed同理按可信mean升序，再R降序/price/stable ID。其余模式无主样本时整体降级到price规则；全部候选均无可信指标时综合亦price降级，显示cold_start原因。不可信候选不以伪造高成功率赢过可信候选。

综合仅在距离最高score≤0.02的近同分集合分摊，权重w=max(0.01,score)/(1+active)，active未知时仅此分母取1且保留unknown标记。将近同分候选按group UTF-8字节序排列，SHA-256输入为长度前缀编码的算法版本、服务端随机request ID、attempt序号、client_model和该候选组序列；取摘要前53位无符号整数除以2^53得u∈[0,1)，用u×Σw命中首个累计权重严格大于它的候选，浮点边界落最后一项。其余组按score降序/组ID排序。相同请求种子、attempt及观测快照得相同结果，新请求不同种子形成并发分摊；用户提供的request header不能操纵种子。其他三优先模式不在不同主值之间随机打乱。近同分之外不以“负载均衡”随机选明显差组。

### 5.3 短时健康

每(K,stage)在近60s连续3次可归属上游失败，或近5min eligible≥20且失败率≥50%，软下线30s。重复触发冷却翻倍至120s；成功受控探测后恢复，连续5min无触发重置基础冷却。429在60s中n≥5且占比≥20%，或同口径active达到过去5min成功完成并发峰值且≥2，标“拥挤”；无容量依据只显示观测负载，不伪造排队时间。5min并发峰值没有样本时不使用active阈值判拥挤，L仍可调节综合分。

连续失败/冷却不能从分钟总数倒推。mowan_routing_health按(K,stage)唯一，主DB行保存version、递增event_seq、连续失败尾部最多3个时间、300个一秒短窗槽、state=closed|open|half_open、cooldown_until、backoff、breaker_epoch、probe_lease_id/probe_until和最后触发时间。跨实例收到finish以主DB事务锁/CAS成功应用顺序为唯一事件顺序（不是节点墙钟或上游完成时间）；时间由同一DB时基产生。mowan_routing_health_events以attempt/terminal event_id唯一去重，重复报告不推进event_seq或短窗；延迟超过5min的首次报告仅进长期指标，不改当前健康。eligible success清连续失败链，ignored不增加也不打断；链首距当前>60s先清理，再判断三连败。5min失败率和60s429从秒槽计算，空槽自然过期，不使用分钟桶近似。

冷却到期时，在同一health行事务中CAS open→half_open并递增breaker_epoch，写唯一probe lease；每(K,stage)全实例只一claim，未取得claim就跳过。claim仅由真实有预算请求取得，不后台生成收费请求；attempt probe随提交attempt每10s续租、30s过期，以该提交的真实结果裁决attempt breaker。持有者退出/过期才允许新epoch的claim；结果携带epoch和lease，旧claim迟到结果可记统计但不能关闭或覆盖新breaker。当前epoch的probe成功关断路器、清连续链；probe失败/租约失效回open并按30→60→120s冷却。显式try_soft_offline同样遵守各stage的真实成功定义，不能用受理成功关闭task_terminal breaker；过期epoch不能复活状态。管理员禁用绝不通过探测复活。

异步task_terminal probe与提交attempt probe是独立的claim，发送前将terminal的K/epoch/lease关联submission的prepared耐久凭据；两个breaker均需放行时须分别取得claim，未发出的已获claim释放回open且不记供应商失败。accepted独立保存事务同时耐久关联terminal probe到该submission及可空Task；提交accepted只结束attempt probe，不结束terminal probe。由现有任务查询/恢复系统任务观察该submission（Task尚未入库也可恢复），仅查询/保存，不调用创建接口。观察者owner及generation存于health行，5s续持有租约、15s过期；其他节点在owner释放或到期且probe_until仍有效时CAS接管并递增generation，旧owner不得续租或裁决。有效观察者每10s续terminal probe的30s lease，续租以DB中accepted且未终态事实为依据，不以进程还活着续租；真实终态以terminal event去重并匹配epoch/lease裁决成功/失败。probe最长持有24h，期限到或probe lease失效回open递增epoch、冷却并告警，仍继续原任务查询/资金收尾，不取消任务、不退款、不重建；其后迟到终态只记统计，不关闭新epoch。

提交not_sent/rejected时未产生终态，释放terminal claim回open、保原backoff，不给terminal成功或失败计数；提交unknown时同样回open并按现有backoff冷却、使旧terminal epoch失效，预留和凭据按§6.1保留，不能把unknown算终态成功。随后查询得到accepted可关联Task，但不得抢回已失效terminal claim；只记录最终任务统计。所有claim转移/释放都用epoch/lease/version裁决；两个stage不会因提交受理或释放active lease互相关闭。

健康状态/claim每次选路读权威DB，分钟排名缓存不代替claim裁决；DB健康操作失败显示unknown、只按硬合法集合既定降级，不伪称normal/探测成功或扩大权限，资金/鉴权仍按各自失败关闭。所有epoch、事件和短窗均在现有GORM/lease接点实现，不建立通用熔断服务。

### 5.4 多实例与有界存储

mowan_routing_metric_buckets完整唯一键见§5.1；进程只上报累计绝对快照及单调revision，GORM条件更新拒绝旧revision，重复flush不加倍。本地finish once与任务终态CAS去重；单节点重启丢未flush至多5s长期观测，明确是advisory误差，不影响资金/权限；即时health是另一路逐事件事务，不受分钟flush延迟。桶及health event去重记录保留48h后系统任务清理，晚于保留期的重放不更新实时健康。health状态/租约不随分钟桶删除，idle closed状态在无有效租约/活跃任务且无48h事件后可清理；未恢复open保留状态，不自动按清理视为健康。

mowan_routing_leases按lease_id唯一并索引完整K/stage/Channel/expiry，start写入、10s续租、finish删除，TTL30s；聚合active只算未过期租约。DB是所有部署的租约/health/半开权威，Redis仅可缓存≤5s的只读统计，不另设第二个可写claim。async受理后释放attempt提交lease，长任务active从Task及accepted/unknown凭据按submission_id去重聚合，归原K/task_terminal，不把提交lease充当数小时在途；凭据关联Task后只计一次。指标/active统计缓存≤5s；权威claim不缓存。观测失效则active/健康unknown并按已定义降级；策略/鉴权/资金读写失败仍失败关闭。

### Scenario: 真实指标与窗口
Acceptance: A12
Given 先失败后成功的relay及异步提交失败/受理/终态，When 聚合，Then 各attempt和终态各记一次，完整K/stage在桶/lease/评分/health一致；不同provider/operation不混速度，混合variant标未知，缺失/过期明确。

### Scenario: 四策略及冷启动
Acceptance: A13
Given 已知成本/成功/速度/负载及缺样本、过期、全不可比输入，When 评分，Then 符合本节权重、公式和降级，无NaN或未知值当最好。

### Scenario: 即时故障和恢复
Acceptance: A14
Given 跨节点交错成功/失败、重复/迟到事件或短窗429，When 下一次请求选路，Then 按主DB应用顺序计算三连败、成功打断且不等分钟flush；冷却/半开claim/租约过期及旧epoch结果符合本节；长任务accepted不关闭terminal breaker，观察者接管、unknown/rejected、24h到期和迟到终态不重建、不影响原资金收尾。

### Scenario: 可复核分摊与多实例
Acceptance: A15
Given 同一观测快照/请求种子与多实例租约，When 评分/flush重复/节点退出，Then 排名可复核、近同分可分摊、无重复累计或永久active。

## 6. 协议安全和重试

安全重试必须同时满足现有ShouldRetry语义、剩余全局预算、可重放body、未向客户端提交输出，以及该操作明确不会产生重复任务/扣费。网络错误不能一概视为安全；同步纯推理在无输出且现有契约允许重试时可重放，资源创建/异步/带副作用工具操作在已发出而受理状态不明时停止，返回明确错误/请求ID供追踪，不盲建第二任务。明确未受理429/5xx只有adapter/现有错误明确标可重试才fallback。用户校验400/401、策略/资金失败不作为供应商故障绕过重试。

HTTP stream在首个字节/事件/心跳已经提交后禁止切组，不能只用是否有文本token判断；关闭原stream或按原协议发送终止错误，不拼接第二模型结果。提交标记请求级单调保存，不能因每attempt重置SendResponseCount而清除，WS实际socket写入也须登记。下游WebSocket升级本身不证明已提交业务请求：Responses WS在首次上游连接成功前、未发业务事件时可安全dial retry；一旦上游会话建立或response.create发出即固定，不在同一会话拼接另一组，后续create重新鉴权/预留但复用连接。Realtime同样区分dial前/已连接/已交换数据，不能依靠HTTP Writer判断。Responses previous_response_id、严格会话、origin task、任务查询/续作继续原绑定，超出候选或原Channel禁用按现有明确错误终止。

异步受理成功立即固定task→group/Channel/billing快照；轮询只追踪原任务，终态失败也不能在后台重建昂贵任务。客户端主动重新提交是新请求，另受现有幂等约束，不冒称透明exactly-once上游。日志记录策略版本、候选原因、attempt数/拒绝原因和成功组；物理Channel细节限admin_info，不对普通用户暴露。

### 6.1 异步提交状态与耐久屏障

本节新增耐久提交合同适用于新智能异步请求，覆盖已批准的Task Plugin和智能MJ等真实入口。旧Key通用Task保持既有合同，不新增prepared/sending/accepted/unknown全链接入；不能仅删除旧退款defer或伪装成smart而留下无主预留。MJ专用路径与通用Task分别按基线及本轮改动核验，不以通用Task排除项免除智能MJ闭环或本次引入回归的修复。

各transport/adaptor显式报告not_sent/rejected/accepted/unknown，不由HTTP 5xx推断。not_sent可重选；rejected仅在明确未创建且可重试时重选；accepted绑定原任务；请求可能已写到上游但超时、断线、2xx解析损坏为unknown，立即停止自动重试。通用JSON任务、插件SSE、Responses桥与MJ均遵守，不能只给SSE加NoRetry。无上游幂等/查询能力时不能自动恢复创建。

新智能异步请求使用最小持久凭据mowan_routing_submissions：随机submission_id唯一、request_id/attempt_no唯一、token/user、完整K/Channel、状态及版本、资金来源/预留关联、定价快照、可空upstream_task_id/local_task_id与时间。无prompt/body/凭据；未知任务ID可以为空，用户以网关submission_id追踪。先生成稳定submission/operation身份，再在同一主DB事务写初始funding operation、钱包或订阅预留、有限Key预留及prepared提交凭据（免费也保留0预留记录）；任一步失败全部回滚，不留无主预留且不发上游。切候选补预留与该attempt的prepared关联也同事务，不把已存在预留重复扣一次。

prepared耐久提交后才能CAS prepared→sending；CAS确认的提交资格、attempt序号与预算占用绑定，失败或未取得唯一资格不发送。事务提交前崩溃无预留；prepared提交后、sending提交前崩溃可证明未发，恢复以唯一operation释放或按原请求仍有效时继续同一次CAS；sending提交后而调用上游前崩溃亦保守unknown，不能证明未发送时不退款。调用后accepted保存前崩溃同样unknown。每个恢复动作以状态version/operation唯一键裁决，不能由“进程刚启动”猜测状态。

- 明确not_sent/rejected：允许后续候选复用同一请求级资金会话；最终无候选时退未消费预留。
- accepted：先独立事务耐久保存上游身份及最小可恢复提交结果，再另一个事务插入本地任务并关联资金/凭据。前一事务不与Task插入同回滚；所需材料包括既有Task创建字段、provider执行快照/私有状态、提交时quota和价格上下文、即时结果引用等，沿既有Task私有数据保护与大小约束，无凭据/prompt和非必要原始响应。不能保存完整恢复材料时保守unknown并告警，不谎称可自动补建。客户端断开不取消耐久收尾；Task插入失败保accepted凭据重试本地保存，不重发上游、不按HTTP错误退款。上游已受理但accepted事务未提交即崩溃时，sending按unknown处理。
- unknown：保留预留及凭据，不作为成功扣整单、不自动退款或再次创建；错误携带submission_id与状态，控制台任务/审计可查脱敏状态。保留到原provider查询确认或管理员带原因对账，禁止按超时直接判未受理。没有查询能力时明确“需人工核对”，不假造可恢复。
- 已持久任务：终态按原快照一次结算/退款；重启恢复只做查询、保存和幂等资金收尾，绝不再调创建接口。prepared且未进入sending的孤儿预留可证明未发，再退。

对账复用现有系统任务/任务列表和管理员审计，新增窄范围查询/处理接点（/api/mowan/smart-routing/submissions，owner只读自己的脱敏状态；admin处理必须现有Casbin+PAT和预期版本/原因）。处理命令仅确认已发生事实并幂等结算/退款，不提供“重新提交上游”按钮；未知凭据与未完成资金不能按48h指标清理。

用户在现有/usage-logs/task列表及任务详情查询，包括无local_task_id的凭据行：该行稳定主键为submission_id，不伪造已存在Task，也不依赖task_id才能打开详情；关联Task后合并成一行，保留submission_id追踪入口。列表/详情展示submission_id、可空任务编号、受理状态、预留额度及单位、最后更新时间、资金结果“处理中/需人工核对/已结算/已释放”；无上游查询能力或长期unknown为需人工核对。未知预留0也如实显示，不显示成已扣账单。错误响应及体验页提供到该行的入口，不将unknown包装为普通失败或推荐再次创建。owner只能读取脱敏信息和处理进展，admin对账状态更新有原因/操作者/时间；不得泄漏物理Channel、私有provider状态或凭据。

### 6.2 源码入口覆盖矩阵

下表是基线6b59db1a41a1eaef05e64bd2b9c4ea976e1c34eb的静态调用调查；A8/A9/A10必须逐行有定向证据，不代表已经实现或测试通过。

| 入口 | 初选/重试接点 | 必须补齐的屏障与资金接点 |
| --- | --- | --- |
| 普通文本/Claude/Gemini/Responses/图音/embedding/rerank/search HTTP与SSE | middleware/distributor.go:100 → service/channel_select.go:303；controller/relay.go:298后续直接Cache路径需回共享裁决 | relay/request_billing.go:53、service/tiered_settle.go:206不能只更新GroupRatio；客户端输出单调屏障 |
| Task Plugin共享Responses/图/视频与原生任务 | middleware/task_plugin.go:520预选、:640模式/:869逐候选decode；controller/relay.go:508 → relay/relay_task.go:250/:352 | Group/CandidateID/Generation复用；provider重定价和发送前安全预留；JSON/SSE四状态、耐久凭据与终态 |
| Responses WebSocket | controller/responses_websocket.go:32每create鉴权；relay/responses_websocket.go:297首次dial retry、:938选择器 | 首连接预算、:344已发create停重试、:551后续连接复核；每create独立预留，不迁移已建连接 |
| Realtime | controller/relay.go:206 → relay/websocket.go:31 | 上游dial/连接/双向数据显式阶段、真实socket输出屏障、原实时用量计费 |
| Midjourney/SwapFace新提交 | controller RelayMidjourney → relay/mjproxy_handler.go及RelaySwapFace | :538余额读取不能代替原子预留；:554发送前进入共享资金/预算；:641插入/:648后扣改为耐久关联和幂等结算；1/21/22等受理码保真实语义 |
| origin续作/remix/MJ变体、查询/轮询 | relay/relay_task.go:47/:152、middleware/task_plugin.go:1327、relay/mjproxy_handler.go:484 | 归属/原Channel/原组/执行器锁；同渠道换key只按明确pin例外；查询不启动智能新任务，收费续作仍先预留 |

MJ智能新提交不能留在“查余额→发送→后扣”旧路径；适配其按次模型/动作定价到共享预留、四状态和凭据。原MJ不支持的订阅资金源仍按明确既有错误发送前拒绝，不扩大资金产品支持范围；钱包/有限Key及原无消费动作保持原计费定义。该矩阵所有可发送入口均在本change完成，不以Phase 2延期协议安全或unknown资金闭环。

### Scenario: 输出/受理边界
Acceptance: A9
Given 新智能stream已输出、上游WS已连接或异步已受理/受理不明，When 发生错误，Then prefer也不跨组拼接/迁移/重建；输出前且原协议明确安全时prefer才在预算内回退，strict已有绑定失败始终停止；pin/origin保持。旧Key通用Task既有异常不在本轮扩围，本次新增回归不能借此保留。

## 7. 发送前资金协议

完整链：校验输入→当前候选/物理Channel→SetupContextForSelectedChannel→解析最终UsingGroup和provider/模型→现有计费引擎估计/冻结→账户/有限Key/订阅/分组限流核验和原子预留→唯一attempt发送→成功实际用量结算/失败退款。middleware初选不算一次真实发送，若task decoder最终改provider/参数，必须在最终发送前再核对。

每次换组或同组provider/最终请求数量改变，都重算当前组生效PriceData和快照中组相关字段；tiered表达式与请求参数冻结规则保持既有契约，组倍率更新不误用旧价格。免费首组切付费必须创建billing session；切贵补足差额原子检查，失败不发送；切便宜不提前释放高预留，结束后幂等退差。安全接点复用service.PreConsumeBilling→Billing.ReserveBeforeSubmit，不能调用普通Reserve充当余额门禁：后者允许trust旁路及普通钱包欠费。订阅按最终组/模型的既有可用范围重新核验，不能通过换组偷用订阅或钱包。

普通倍率、按次、阶梯表达式、图片数量、音频/realtime及任务provider计费都调用同一“最终尝试准备”不变量，不只修PrepareTieredBillingForSelectedGroup。实际收费遵守原协议计费事件：成功组实际usage；失败/中断已有应计usage按既有政策，不为本需求抹除真实已发生费用；未受理失败不能重复收整次请求。任务按提交快照终态结算，不用后改Key重新定价。

所有数量沿既有上下界；Quota*Checked饱和与审计保留，不能裸int转换导致负charge。预留保证发送前估算被覆盖，不承诺实际usage超估算时绝不欠费；实际差额仍按原政策结算并审计。分组独立限流每次新组发前执行，保账户级总门禁一次、候选组门禁逐次，不因切组重置用户全局预算，也不重复扣成功请求槽。

跨崩溃幂等不能只凭BillingSession mutex或“退款后清Task.Quota”。新智能资金使用请求/任务关联的稳定operation_id和mowan_routing_funding_operations唯一凭据，把该次钱包或订阅delta、有限Key delta、任务额度标记/提交凭据状态放同一主DB事务；失败整体回滚，重复operation读取已提交结果。通过现有model原子额度接点增加最小tx参数复用，不另造价格/资金算法。内存中调用Refund、终态CAS、恢复任务与人工对账必须落同一操作身份；LOG_DB记账日志可用operation_id去重/补写，日志失败不能重扣资金。

同一账户的新旧Key不能保留两个余额权威源。本版本智能路由启用前统一使用DB作为钱包/Token/订阅权威，所有Key的余额预留/扣退（含旧固定/Auto、任务、充值/退款/管理员改额）通过既有model共享接点同步落库，Redis只作展示缓存，不能Lua先扣余额再异步batch，余额batch禁用，其他非余额统计可沿原batch。旧Key调度/计价产品语义不变，共享资金底层必要调整不等于迁移旧路由。禁止仅新smart绕过Redis而旧请求仍缓存预扣；钱包和Token资金行按固定顺序锁/CAS，缓存延迟或丢失不得改变可支付判断。

### 7.1 启用迁移与不能自动证明的旧进程状态

当前基线model/utils.go:83–99在写DB之前交换并清空map，失败仅日志，:121–124仍可能打印finished；因此map变空、等几个batch周期、单条finished、DB暂时等于Redis均不证明历史delta已可靠落库。原二进制没有可证明的drain回执，不能先重启/升级/kill再指望新进程恢复旧内存。自动启用对这类旧部署默认blocked。

迁移按以下检查点执行，发布另行授权：

1. 枚举全部HTTP/WS/任务轮询/回调/充值退款/管理员改额/定时器/外部写DB参与者；在LB及任务/回调消费层设置维护屏障，外部回调可靠暂存或返回可重试响应，不丢弃资金事件。暂停后台写入、等待在途收尾及异步额度工作完成。旧进程和Redis保持原状、保留日志及可取得的内存/pending快照；读取快照本身不算排空，也不允许调试器盲改map。
2. 每个旧节点只有具备可核验的pending快照/持久资金流水/逐项DB结果，才能在停写下逐项对账：将已提交delta与未提交delta区分，补偿操作用稳定ID且经核对批准，保留节点/批次/用户与Token差异、DB提交证明和最终余额。当前基线日志无逐项成功凭据，失败delta还可能已从内存丢失，缺材料就blocked，保留进程/快照交人工核账；不能根据Redis总额直接覆盖DB或把DB读等于缓存当作充分证据。已具可靠drain能力的兼容版本才可凭“durable pending已提交+全部inflight=0”的逐节点回执排空；此能力不能追溯证明原版先前丢失的delta。
3. 只有全部参与者均完成对账并经组长/运维确认，备份DB和对账材料，才有序停旧实例。部署层移除旧实例启动配置/自动拉起/旧worker，隔离旧部署凭据并轮换为新实例独占DB/Redis写凭据（含所有后台进程），按部署清单及进程/连接核验旧实例不再写；不能仅摘LB，因为其后台仍能扣款。拿不到全参与者清单或不能隔离旧节点仍blocked。
4. 全部新实例用相同同步DB余额模式启动，读主DB模式标记和配置自检；完成后再清理旧余额展示缓存、恢复回调/worker和流量。DB标记只约束认识它的新版，绝不声称能阻止旧二进制；禁止新旧余额模式混部，部署隔离才是旧节点屏障。冲突或对账未完成不启smart、不回退缓存扣款。回滚也需停写排空并核对新凭据/旧schema能力，不进行余额模式热切换。

迁移验收包含真实注入batch写失败仍finished、map已换出、并发回调、缓存丢失和旧worker继续写等反例，必须证明它们阻塞启用。不是每个旧环境都能自动迁移成功；缺证据的历史余额需人工核账，该限制必须显著告知用户。

### 7.2 同步DB容量边界

本轮仅在当前本机及本change已建立的隔离SQLite/MySQL/PostgreSQL等资源验证，不要求用户新增环境。依据2026-11-17用户后续明确决定，本项目今后验收不做压测，不安排阶梯、长窗或其他容量负载；本任务自有负载/采集进程已停止，全部产物及隔离资源保留。交接只引用已有报告中实际存在的事务、锁/池等待、资源、吞吐和错误率记录及覆盖场景；缺失指标标未测，不为填满指标新增执行。报告绑定原硬件/DB版本/连接池/节点数/代码身份、负载和窗口，不能以HTTP时长冒充DB事务时长，同一实现换标签不作为旧版基线比较。必要功能、正确性、安全检查及独立验收不因取消压测免除。

PG30分钟窗口未完成不再作为本轮证据交接前提，不要求继续定位最高吞吐。已有短窗稳定档位仅称最高已测短窗稳定档，不宣称完整稳定容量或生产容量；失败/未测范围继续如实记录，性能失败不能改记通过。已观察到的MySQL4RPS同一400额度差异从现有请求关联、资金journal/结算和采样时点作必要归因，区分未终结在途、测试核算问题和真实回归；若确认属本次引入则修复并进行最小定向验证，不能作为单纯性能问题略去。保留原报告及其失败，不以修正核算反写历史。用户2026-11-17明确同意：本次新增采集器造成且无法补回的历史终态缺证列为已披露限制，仅由该项缺证造成的交接/独立验收阻塞解除；不据此认定历史收费正确，不豁免当前实现缺陷或其他资金证据要求。SQLite只作为单写节点本机/低并发验证，不能由其通过推断多实例容量。

本轮核实来源只用已有报告、日志、基线及代码差异，不新增测试、不扩修；已有最小正确性检查按其版本/范围复用。历史stream200/retry100及同类已存档终态缺口的采集来源，与对应生产计费是否正确、是否本次引入分别记录。已证实非本次引入的问题可保留；本次新增采集器的限制不能当作既有产品缺陷，未知产品问题不自动视为既有。用户对上述无法补回历史缺证的明确接受仅为继续独立验收的窄范围授权：原始报告及Builder20/22 failed、历史收费正确性未知均保留，当前实现的功能、资金、权限、协议安全要求不变。先沿同一Run提交正式候选及A1–A22证据映射交组长复核，暂不启动Verifier；不包含accept、Git操作、部署或清理授权。

实际部署容量和历史运维材料缺证如实披露，不用本地fixture收据代替，不外推生产。本项目验收不以补跑30min、定位最高吞吐或其他压测作为动作；原30min稳定吞吐和预计峰值70%的容量声明未获证实，不因取消压测而声称达标或已满足原启用条件。§7.1历史核账、旧写节点隔离、禁混部及回退安全条件保持，缺证独立列正式启用限制；本轮交接与部署条件分开，未授权部署。入口并发仍受连接池及现有限流约束，超容量发送前明确忙碌/限流，不能无限排队持锁调用上游，不自动改回Redis权威。

资金故障注入覆盖扣资金后/扣Key后/清任务标记前/提交后断线及重复恢复；并测试同账户legacy+smart并发、切换前pending batch、Redis故障/缓存延迟、充值退款并发。不得用operation去重代替余额串行化，三库一致性未证明不能通过A10/A21。此项影响共享资金接点，已有支付证据仅复用未变化部分，相关并发回归属于本需求新增风险检查。

### Scenario: 切贵组先核资金
Acceptance: A10
Given 免费/低价组失败及更贵候选，When 钱包、Key或订阅不足或数量增大，Then 补预留失败且未发送；资金足够时按成功组实际usage结算，重复回调/退款不增减第二次。

## 8. UI与临时体验

现有Key抽屉组合共用策略区；“怎么选”弹层完整解释四策略、适用场景、窗口、失败可能换贵、首选预估与实际结算、固定分组边界和未知降级。不写“永远最低/最快/必成功”。动态模式提供排除模型、全局组和模型内组的选择器；说明新增获权自动参与。

自定义第一步：模型名/显示名搜索，全部/文本/图片/视频/音频分类，当前模型候选卡含生效规格价格及单位、价格复制、原始成功率n/window/状态、近1h响应类型和样本时效；健康筛选all/normal/congested/soft_offline/unknown；全选健康只加入当前筛选结果中normal项，不自动清除已有选择。“清空当前模型”从已选models配置移除该模型及其组/调度草稿，保留其他模型，不留可提交空壳；手动取消该模型最后一个组等价移除。仍可在模型目录重新进入并从空选择开始；至少另一模型有效时可继续/保存，全部清空禁下一步/保存并提示至少选一个模型及分组。API直接传空groups的模型仍400，不能静默删模型掩盖无效输入。已选清单由独立选择集合维护，不随筛选/分页消失；失效已选项可见且可删除。

第二步按模型配置四动态或manual，可批量设置调度方式但最终每模型独立值；manual拖拽与上/下按钮等价，键盘可用，首尾禁用且焦点不丢。每模型“临时健康下线仍尝试”仅在该模型选中候选内生效，说明不覆盖硬禁用。返回第一步保值，弹层保存只回写父表单草稿，父表单最终保存才生效。空、失效、超限、冲突服务端错误映射字段；保存中防双提交，失败保留输入。

首页选择模型后的路由区、模型广场详情的体验入口、/playground共用控件/策略解释/报价服务；访客仅展示公开价格和登录引导，不获取私有候选。登录体验沿现有/pg/chat/completions的UserAuth会话链，携带body中mowan_routing临时对象，由UserAuth后、Distribute前的窄中间件提取、校验并剥离，不传给上游；同§2的大小/候选/模型权限校验，controller.Playground生成临时Token不得覆盖该请求已验证RoutePlan。只作用该次请求，普通/v1 Key端点拒绝该覆盖字段/header，不把普通playground声称为某个持久Key策略测试。账户资金仍真实核验计费，playground按原定义无有限Key扣款。临时自定义白名单同样经过服务端校验；页面切换/取消不调用Key策略保存API。尚无执行能力的音视频入口维持现有提示，不新增创作系统，但其Key API调度仍按真实任务协议支持。

### Scenario: 选择器保值与状态
Acceptance: A5
Given 已选跨模型候选，When 搜索/分类/健康筛选/分页/清空当前模型，Then 其他选择保留；清空移除该模型而非留下空壳，全部清空阻止下一步/保存，可重新添加；失效项不因筛选消失。

### Scenario: 首页/体验的临时策略
Acceptance: A17
Given 首页或模型体验临时改策略，When 试算/提交/取消，Then 价格与解释同Key口径、只作用该次体验、不持久改Key。

### Scenario: 七语言和可访问交互
Acceptance: A20
Given 手机/桌面和七种界面语言，When 加载/错误/空态/排序/键盘导航/保存冲突，Then 状态完整、翻译与数字格式正确、焦点/label/aria可用，有真实行为回归。

## 9. 持久化与API契约

mowan_token_routing：token_id唯一（关联现有Token）、schema_version=1、mode=legacy|fixed|smart、policy_json TEXT、legacy_snapshot TEXT可空、allow_self_write bool默认false、version int64≥1、created_at/updated_at。无扩展行legacy的version=0；一旦显式保存扩展行，包括恢复legacy，都递增版本不回到0。version通过WHERE token_id=? AND version=?事务更新，不用JSON数据库专有类型/函数；创建Token与路由行一事务，删除联动清理。model.Token.Insert/Update当前用全局DB，必须提供真实tx接点，不能仅外面Begin伪称同事务。批量创建/删除覆盖同样边界，失败不留下孤立Token/配置；耐久提交/资金凭据仍保留脱敏关联供结算，不随Key删除丢失。缺行与DB错误区分；读取失败503，不当无行降级legacy。策略解析失败显式拒绝。

规范化routing对象例：

```json
{
  "schema_version": 1,
  "mode": "smart",
  "strategy": "custom",
  "models": {
    "example-model": {
      "strategy": "manual",
      "groups": ["group-a", "group-b"],
      "try_soft_offline": false
    }
  }
}
```

动态smart对象包含strategy=balanced|price|speed|success及excluded_models、excluded_groups、excluded_model_groups；不含models。fixed包含fixed_group（允许显式空字符串表示沿用现有继承语义，仅已有legacy固定进入时保留，新用户选择须明确合法单组）；不含strategy/排除/models。非当前模式字段保存时拒绝400而非偷偷执行。响应封装沿本项目success/message/data；Key响应不得回送完整密钥。

控制台沿既有GET/POST/PUT /api/token/扩展routing、routing_version和allow_routing_self_write字段。UserAuth（session或管理PAT）、数据归属和PAT api_key:read/write维持现有能力；普通用户编辑自己Key不新增管理员Casbin门槛，有管理权限接点才复用handlePermissionRoute并登记scope。不因为路由自管理给普通Key注册管理scope。新建默认及旧客户端显式路由兼容按§1。修改策略要求expected_routing_version（legacy起始0），409 routing_version_conflict需重新读取，禁止静默覆盖。用户只改名称可不带版本，不更新策略。写开关变更同版本保护，并记审计。

独立读取候选GET /api/mowan/smart-routing/candidates（console UserAuth）与POST /api/mowan/smart-routing/preview（只读试算，console UserAuth，复用限流/请求清理/模型权限）不保存Key。self身份可GET /api/mowan/smart-routing/self及GET /api/mowan/smart-routing/self/candidates；PUT /api/mowan/smart-routing/self body={expected_version,routing}，只允许改策略，不接收token_id/user_id/allow_self_write/配额/IP/模型授权/过期字段。未知字段也拒绝400，不能靠忽略字段掩盖越权。Key自认证不接受session、PAT、dashboard internal token、渠道后缀或跨Key ID参数。

self GET/PUT均采用真正TokenAuth完整有效Key、账户、IP、失效/余额门禁，并明确拒绝管理PAT、内部dashboard身份和渠道后缀，不套TokenOrUserAuth，也不采用可读过期/耗尽Key的TokenAuthReadOnly。self GET在write关闭时仍允许；PUT在关闭时403 routing_self_write_disabled；开关只能控制台账户授权开启。self允许在现有硬权限内调整固定/动态/自定义，不能删除旧硬模型限制或扩大账户获权组；allow_self_write不属于routing。事务写入同时匹配version、allow_self_write=true、当前Token归属/有效状态；owner关闭/删除与self写共用锁和版本，禁止先读开关后无条件写入复活配置。self限流不消耗上游调用额度，拒绝不预扣。

审计扩充middleware/audit.go既有白名单和AuthMethod=api_key，记录token_id/hash、版本变化和脱敏差异/拒绝原因，不记录secret/body提示词。复用既有独立LOG_DB尽力写语义，失败告警/指标，不承诺配置与审计跨库原子；资金正确性以主DB耐久凭据为准。拒绝审计放认证前可取得安全身份信息的边界，不能只记成功handler。

路由注册从已有api组派生，在SetPluginRouter快照前；继承全局审计、限流/BodyLimit/清理，self独立分组叠加TokenAuth和更窄DTO，不用同前缀另起缺中间件的组。需要新增authz资源时每实例authz.Init前登记，迁移仅master且失败阻止服务，不能把权限登记放master条件内。

### Scenario: 保存模式与CAS
Acceptance: A16
Given 当前模式草稿/并发编辑、绑定TTL到期或组被撤权/排除/配置修改/禁用，When 下一发送，Then TTL到期按无绑定，仍存在但失效绑定的prefer重选而strict报错；SwitchOnSuccess及TTL按既有配置更新真实组+Channel且不污染legacy；只保存当前模式，冲突409，DB失败不回落legacy。

### Scenario: Key自管理最小权限
Acceptance: A18
Given 普通Key访问self，When GET/关闭时PUT/开启后合法PUT/越权字段，Then 只查改当前Key，开关默认关闭，越权明确拒绝且IP/模型/限流继续生效。

### Scenario: 管理认证与审计
Acceptance: A19
Given session/PAT/普通Key各种身份与删除/更新操作，When 调用管理或self，Then 两认证边界明确、PAT/Casbin/归属校验不缺、审计脱敏、跨实例下一发送读最新版本。

## 10. 实施依赖与验证

内部串行任务（一个change）：T1配置/schema/CAS/鉴权与旧Keyroundtrip→T2真实观测/健康/纯评分→T3共享请求计划与全部初选/重试入口→T4发送前资金与协议边界→T5共用UI/体验/七语言→T6三库/多实例/协议资金集成→Runtime检查及全量验收项的独立Verifier。常规模块辅助文件可在已确认目录内按需增补，不建立通用framework。

必须把handler实际调用链和资金准备函数映射到A8/A9/A10测试；HTTP重试不能只改SelectChannelForRequest，因为getChannel目前直接调CacheGetRandomSatisfiedChannel。旧Auto回归与新模式预算分开保护。测试上游使用可控假服务，不调用参考站真实创建Key或产生真实收费。

迁移用GORM显式调用，SQLite、MySQL≥5.7.8、PostgreSQL≥9.6真实环境检查新库、存量升级、重复启动、CAS/唯一约束/并发、事务失败/迁移失败阻启。验证仅使用当前本机及本change已经建立的隔离资源，不要求新增环境；缺环境、指标或真实材料如实blocked，不伪造通过，不把短时本地结果当部署容量或历史运维事实。

根Go及两个独立模块各在GOWORK=off执行必要vet/build/test；web在匹配Bun执行typecheck、涉及文件lint、受影响测试和build。主要覆盖可见行为/边界/资金安全，不堆只证明执行的smoke。UI必须读shadcn技能并检索现有组件/实际用例后组合，七语言用现有t()/toIntlLocale/金额格式，前端行为测试放模块__tests__。最终Verifier复用版本/环境/输入仍有效Runtime收据，只补失败/缺失/变化项；失败、timeout、not-run不计passed。

范围修订继续使用原Native Run，保留实现及仍适用证据，正式文件重新绑定后再继续普通实现。按当前范围核对全部验收项，不因交接重复稳定检查，不将旧Shape结论直接移作新Shape通过；交接分别列出当前环境已执行结果、排除的既有问题、未完成检查和正式启用缺证。对公共资金和协议接点，以基线及本轮diff识别新回归，不能因用户排除既有缺陷而漏修本次引入的问题。

本项目今后验收不做压测，既有性能失败、未测范围和正式启用限制随候选交接；400额度差异的现有归因与已知限制完整披露，本次已确认回归仍须修复，资金、权限、协议安全和独立Verifier保持原要求。本轮仅核实既有材料及基线差异，不新增测试或生产修复；已证实非本次引入限制可保留，另仅按用户明确批准接受本次采集器造成且无法补回的历史终态缺证，不将其他未知问题自动豁免。既有适用检查复用，不因交接重跑；候选先交组长复核，暂不启动Verifier。

### Scenario: 三库事务与迁移
Acceptance: A21
Given 当前本机及本change已建立的三种真实数据库和新/旧库，When 初启/重复启动/并发CAS/事务失败，Then schema/唯一约束/回滚一致，迁移失败停止启动，旧Key不自动改变策略；本项目今后验收不做压测，既有报告绑定原配置/负载/窗口，失败与未测原样保留，不补跑阶梯或长窗且PG30分钟未完成不阻止本轮交接；400额度差异的现有归因与证据强弱披露，本次采集器造成且无法补回的历史终态缺证按用户明确批准列已知限制，仅此项不阻塞候选和独立验收，原失败和历史收费未知保留；当前实现缺陷、本次已确认回归仍须修复并必要验证，其他未知产品问题不视为既有或自动接受；真实历史核账、旧节点隔离/禁混部及实际部署容量缺证独立披露，不宣称其已满足。

### Scenario: 独立验收与证据复用
Acceptance: A22
Given 候选实现和检查收据，When 新Verifier验收全部A1–A22，Then 绑定版本/范围判断并复用有效证据，实际所需测试均真实记录，未执行项保持未通过。

## 11. 回退与交付边界

Shape不修改实现、数据或真实Key。实现完成先经Runtime必要检查和另一个新会话独立验收，再由用户接受结果；归档、提交、merge、push、PR、部署、worktree清理各按明确授权，不互相代替。首次部署前备份数据库。新扩展表回退保留，旧服务无法理解新策略时不能宣称完全等价回滚：需先停止新智能Key流量、恢复兼容版本或显式迁回固定并告知影响，不能悄悄放到default组；旧Key继续原语义。迁移不删除上游数据，不自动重写旧Token组字段。
