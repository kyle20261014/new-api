# iDataRiver 钱包固定套餐支付完整规格

## 入口与固定套餐

在魔丸已有 /wallet 的充值区展示 iDataRiver 支付方式及服务端允许销售的固定套餐，复用充值卡片、确认弹窗、支付方式区和历史订单。每个套餐由唯一 SKU、名称、USD售价、到账面值组成，每单 quantity=1。选择商品不下单，点击继续才创建。无配置、禁用、加载、空套餐和接口错误均有可理解且可恢复的状态；排除测试SKU 6ae41bd5a5befc799a109d36。其他支付方式保持各自原有金额规则。

### Scenario: 固定套餐选择与下单
Acceptance: A1
WHEN 用户在现有钱包选择套餐并点击继续，THEN 服务端校验可售SKU、配置及平台商品状态，创建单SKU数量1订单；仅选择套餐不产生订单。

## 金额与付款

订单保存创建时的用户、商户、项目、SKU、数量、价格、到账额度及回调源快照；前端提交SKU，不能控制售价和额度。订单商品计价为USD；渠道实付可以是其他币种。确认框采用快照，不重复使用通用充值折扣/汇率。渠道必须来自该订单已启用的方法。payCurrency/amount缺失时明确由官方支付页确认，不把缺失当0，不猜手续费。

### Scenario: 明确金额语义
Acceptance: A2
WHEN 确认订单，THEN 分别展示套餐USD售价、到账额度、已知渠道实付金额和币种；未知信息清楚标明。获取pay链接后先留在原确认框供用户阅读，由用户再次点击继续支付后才同页跳转；切换渠道、关闭弹窗、账户/订单状态变化使旧报价失效，迟到响应不能恢复失效报价，重复点击不能重复跳转。

付款接口返回已验证的HTTP(S)官方支付链接；禁止凭空拼接付款URL或嵌入iframe。跳转前持久保存当前账户原订单关联，同页跳转，支付渠道由官方承接。回站参数只用于定位订单。

### Scenario: 同页付款与链接失败恢复
Acceptance: A3
WHEN 获取有效链接，THEN 保存订单并同页进入支付；WHEN 获取失败或URL非法，THEN 保留原订单、显示错误，允许重试取链接而不新建订单。

## 查询和恢复

回站、刷新、重进钱包或页面重新聚焦时恢复当前账户待确认订单并发起查单，采用有上限的重试/轮询，避免重叠请求；终态停止自动查询。确认成功后刷新余额与订单。查询失败保留订单和上次可信状态，不将错误翻译成“未付款”；轮询停止后保留手动查询入口。退出和切换账户隔离浏览器保存的关联；登录失效先重新登录，所有访问均由服务端校验归属。

### Scenario: 回站和账户恢复
Acceptance: A4
WHEN 当前用户回站、重进或刷新，THEN 恢复自己的待确认订单并有限查询，确认成功后刷新余额/历史；其他账户订单不可见不可查。

### Scenario: 无法确认时保留订单
Acceptance: A5
WHEN 查单超时、网络错误或轮询结束，THEN 显示对应待确认/查询错误状态，保留订单和手动查询入口，不判未付款或过期。

历史订单承载恢复，不要求用户记住或粘贴订单号。继续付款每次都先服务端查单：仅本账户订单、上游NEW、本地待支付、未退款、渠道仍启用才允许重新请求payUrl；不无条件复用旧链接。无可用渠道或支付接口失败保留原订单。

### Scenario: 原订单继续付款
Acceptance: A6
WHEN 从历史记录继续付款，THEN 核验原订单NEW及渠道后重新获取链接；已完成、退款、过期禁止继续，且不创建新订单。

## 接口契约

以下是目标项目拟实施契约，与生产旧版 /api/user/idatariver 路径区分：

- GET /api/mowan/idatariver/products：当前用户鉴权，返回 enabled、允许销售的 products（sku_id/name/price/quota/currency=USD）；不暴露商户密钥或匿名查询关联。
- POST /api/mowan/idatariver/order：当前用户鉴权和关键操作限流，输入 sku_id、稳定 request_id；相同用户/request_id返回同一尝试/订单，不能重放平台创建。创建中/结果未知返回明确待核实状态，不伪装成普通失败。正常结果含 trade_no、平台/本地状态、套餐快照和本订单可用渠道；不返回可检索订单的匿名凭据。
- POST /api/mowan/idatariver/pay：当前用户鉴权，输入 trade_no、method；查验归属及NEW状态后返回 pay_url、可空 pay_currency/amount，失败不删除原订单。
- POST /api/mowan/idatariver/check：当前用户鉴权，输入 trade_no 或 request_id（二选一）；解析本账户订单/尝试，服务端权威查单并幂等协调，返回业务状态、到账状态、恢复操作能力；查询错误与正常NEW分开。
- POST /api/mowan/idatariver/notify：平台公开回调入口；通知可为订单对象或现有result信封，不信任其金额/状态，按平台订单ID定位本地快照再服务端查单。按官方5秒处理预算返回HTTP200/201，仅在完成安全处理时确认；失败保留可重试性。
- 既有钱包订单记录接口扩展本账户 iDataRiver 摘要及恢复所需状态/操作；未能拿到平台订单号的本地创建尝试也保留可恢复标识，不能凭缺少trade_no静默消失。

生产参考的上游接口不变：POST https://open.idatariver.com/mapi/order/add 输入 projectId、skuId、orderInfo={quantity:1,coupon:"",contactInfo:<服务端关联>,affCode:""}；POST /mapi/order/pay 输入 id、method、redirectUrl、callbackUrl；GET /mapi/order/info 输入 id；GET /mapi/order/search 输入query，仅用于服务端关联核实。商户Bearer认证仅在服务端。redirectUrl为配置站点/wallet?trade_no=<原订单>，callbackUrl为配置站点/api/mowan/idatariver/notify。平台的创建幂等、链接有效期、原生二维码不在契约承诺中。

## 状态、退款和可信入账

上游权威状态为NEW、DONE、EXPIRED、REFUND。服务端收到通知后重新调用order/info验证；通知正文、回跳和按钮均不证明付款。EXPIRED才认定过期，浏览器倒计时、旧链接失效、网络错误不改变订单终态。仅在没有退款处理记录时，迟到DONE通过完整匹配仍允许一次入账。

退款仅接收平台结果并处理本地账务，不增加用户发起退款入口或人工退款操作界面；沿用参考实现 model/idatariver_order.go:125-200 的规则：

- REFUND先于入账：记录before_credit，该订单以后不得入账。
- REFUND发生在入账后：以该订单已入账额度为回收目标；当前余额足够则在同一事务一次全额扣回并记录recovered，不按当前套餐配置重新计算。
- 当前余额不足：不扣除任何额度，不部分扣回、不透支；记录manual_required并保持可审计，交人工处理。
- 退款记录一旦为before_credit/recovered/manual_required，重复REFUND或迟到DONE均不得再次扣款或入账；manual_required不因后续充值使余额足够而自动重扣。
- 数据库操作失败时整个退款事务回滚，不留部分扣款或已处理标记，后续可信查单可重试。

### Scenario: 过期、迟到确认与退款
Acceptance: A7
WHEN 上游确认过期，THEN 停止支付并允许用户主动新建；WHEN 尚未处理退款且收到可信迟到DONE，THEN 至多入账一次；WHEN REFUND先于入账，THEN 记录before_credit且后续DONE不入账；WHEN REFUND在入账后且余额足够，THEN 一次全额回收并记录recovered；WHEN 余额不足，THEN 零扣回并记录manual_required；WHEN 重复REFUND或任一退款处理后迟到DONE，THEN 余额保持不变且不自动重扣；WHEN 数据库失败，THEN 全事务回滚并保留重试性。界面不新增用户发起退款入口。

创建尝试应有稳定本地关联，重复点击/重试不重放外部创建。外部创建超时可能已经成功，先保留待核实尝试；服务端保存受保护的必要关联信息，通过可信查询只恢复唯一且全部匹配的订单。无法证明成功或失败时维持待核实，不能自动新建；不得声称平台支持未公开的幂等键。历史订单已知时直接按原订单恢复。

### Scenario: 创建结果未知
Acceptance: A8
WHEN 创建响应丢失或超时，THEN 重试回到同一尝试并查询关联订单，唯一完整匹配才能恢复；无证据则保留待核实，不自动重下单。

结算须核对商户、项目、SKU、数量1、原价和折后价、商品USD语义及用户归属，拒绝不匹配及无效渠道。事务、行锁/条件更新和余额安全边界保证并发通知/查单只入账一次。不得泄露商户密钥、匿名查询凭据、数字商品内容或用户资料。调用使用服务器配置域名、HTTPS回调和目标/wallet回站，不接受客户端回调地址。

### Scenario: 信任边界与幂等
Acceptance: A9
WHEN 伪造通知/回跳、串户或订单不匹配，THEN 不错误入账；WHEN 合法DONE重复并发到达，THEN 余额只增加一次且敏感信息不外泄。

## 设置、兼容与验证

设置页复用既有权限和选项机制管理启用状态、密钥、商户/项目ID、回调域名、SKU及售价/额度，秘密脱敏。目标新增业务集中internal/mowan/idatariver，API挂载/api/mowan/idatariver并继承鉴权/审计/限流；扩展表mowan_前缀，通过主节点幂等迁移，失败阻止启动。现有钱包表和安全换算复用，避免循环依赖。

用户要求功能分支从dev创建，当前基线c76ef65749d89d780161528a6ead15162a12aba8；唯一change idatariver-wallet-payment。生产只读，本次不部署、不修改生产配置、不创建真实订单；测试SKU不纳入可售配置。正式规格和实现通过后另行决定Git交付，不因Runtime残留main交付目标而执行合并。

### Scenario: 配置与兼容
Acceptance: A10
WHEN 管理员保存合法/非法配置或系统在SQLite/MySQL/PostgreSQL新建/升级/重复启动，THEN 配置边界及订单余额完整性成立；WHEN 使用七语言、键盘、桌面和移动端钱包，THEN 可完成同一流程且现有支付回归通过。自动化模拟平台，不将未运行检查记为通过。

## 人民币参考展示

平台显示币种 quotaDisplayType 为 CNY 时，固定套餐卡片、原支付确认框及 iDataRiver 历史金额区域保留套餐售价 USD、到账额度 USD，分别补充人民币参考值。从历史恢复订单时，确认框采用原订单 USD 售价/到账额度快照，参考值使用当前平台显示汇率。平台为 USD 时不附加人民币；TOKENS/CUSTOM 保留既有 USD 展示，不扩展其他币种。

显示汇率来自现有 /api/status 的 usd_exchange_rate，经 status-query 映射至 system-config-store.currency.usdExchangeRate；不使用充值 price、priceRatio、通用优惠或渠道费率。复用现有来源和格式化能力，只进行一次显示换算，不新增汇率服务或修改全站换算策略。卡片、确认框与历史金额订阅现有币种/汇率配置，配置更新或语言切换后同步显示；沿用 toIntlLocale 的七语言数字格式。

参考区域明确标注“人民币参考值按平台显示汇率折算，仅供参考，实际付款以渠道为准”。渠道实付仍按 pay.amount/pay_currency 原值显示，包括极小非零值；缺失仍由官方支付页确认，不用人民币参考值替代。

汇率来源必须存在且为有限正数，换算结果必须有限。缺失、0、负值、NaN、无穷、不可解析或换算溢出时，保留 USD 并提示参考值暂不可用，不阻塞原支付。不得把数据映射/通用工具回退为1的行为当作已核实汇率。正小额须保留非零有效位或明确标注小于阈值，不能显示为0，也不能将工具抬高后的最小阈值冒充精确值。乘积下溢为0时也不能显示虚假零参考值，应按不可用降级。

人民币数字仅用于显示，不进入创建/支付/查单请求、SKU、quantity、订单价格快照、quota或结算/退款计算。报价可读停留和用户主动同页跳转、原订单查询/恢复/过期/退款与安全规则全部保留。

### Scenario: CNY参考展示及结算隔离
Acceptance: A11
WHEN 平台为CNY、有效显示汇率为7.25、套餐售价10 USD且到账额度12 USD，THEN 卡片、确认框与对应iDataRiver历史金额保留10 USD/12 USD并分别显示约¥72.5/¥87（依当前语言本地化）和参考提示；WHEN 当前汇率改为8，THEN 参考值更新为约¥80/¥96而不创建订单或改变快照；WHEN 历史恢复，THEN 使用原订单USD快照及当前显示汇率；WHEN 平台为USD或TOKENS/CUSTOM，THEN 不附加人民币；WHEN 汇率缺失/非法、换算非有限或正小额下溢，THEN 保留USD、提示参考值暂不可用且不阻塞支付；WHEN 可表示的正小额低于默认显示精度，THEN 保留有效位或明确小于阈值，不显示虚假0或抬高的精确值；WHEN 渠道返回0.0002 BTC、72 CNY或空金额，THEN 保持渠道原币种、有效精度或原未知提示；WHEN 下单、付款、到账或退款，THEN 请求、SKU、quantity、订单金额和quota计算不受参考值影响。七语言及切换、桌面/移动端可阅读，原键盘付款与恢复行为保持。
