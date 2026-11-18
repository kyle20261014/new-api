# 单宿主试运行发布契约

本契约是投产前置，不代表已验证线上环境。仅适用一台固定 Linux 宿主机、一个 PostgreSQL 主库和一个候选网关进程；默认 `sync_db_v1` 保持原有严格门禁。试运行模式为 `sync_db_pilot_v1`。

## 进程生命期写入独占

候选在连接数据库之前获取 `/run/mowan-pilot/writer.lock` 的非阻塞排他 `flock`。必须是单独的只读文件挂载，文件为 root 所有、0444、单硬链接，底层为本机 ext4、XFS 或 Btrfs。候选不创建文件、不接受环境变量换路径，也不在业务关闭或 PG 失锁时释放宿主锁；内核在进程死亡时释放。已有 PG 会话锁、旧角色校验和失锁退出继续保留。

发布管理员必须在固定宿主上**只初始化一次** `/var/lib/mowan-pilot/writer.lock`，父目录 root 所有且仅管理员可写。存在时检查，不使用覆盖、删除重建、原子替换、轮转或每次发布的 `touch/install`。记录宿主身份、文件系统、源文件 device/inode 及部署配置版本。容器升级、重启和故障恢复始终挂载这一源文件；不得使用每个容器自己的文件、匿名卷、不同宿主目录或网络文件系统。

对实际部署 Compose 合并以下约束（不是直接运行仓库演示 Compose；演示配置启用了 batch）：

```yaml
services:
  new-api:
    privileged: false
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    environment:
      BATCH_UPDATE_ENABLED: "false"
      ENABLE_PPROF: "false"
    volumes:
      - type: bind
        source: /var/lib/mowan-pilot/writer.lock
        target: /run/mowan-pilot/writer.lock
        read_only: true
        bind:
          create_host_path: false
```

验收实际合并配置和容器 inspect：同一源文件、只读目标、无特权/挂载能力、无 Docker socket/宿主目录管理权限，无其他可写挂载能修改锁源或父目录。应用应直接作为容器主进程运行，不交接锁 FD、不使用保留旧进程的热重载。特权发布管理员属于信任边界；程序不能证明外部挂载源始终未被管理员替换。

容器正常退出、强杀或重启：旧进程未死前，任何挂载同一 inode 的新候选均启动失败；旧进程死亡后才可接替。宿主卡住或容器处于不可终止状态时保持锁占用，先隔离/确认旧宿主死亡再恢复，不在另一台机器创建新锁抢占。宿主重启后旧进程全部终止，仍使用持久源文件启动。**不支持跨主机故障转移或多副本滚动发布**；新增主机必须先引入业务写入栅栏，不能复制本文件后宣称独占。

## 数据库与非候选写入边界

在开放新写入前停止旧网关、后台任务、定时服务、插件/脚本等写入者。旧 DB 登录设为 NOLOGIN，终止并确认全部旧会话消失；检查继承关系和 SET ROLE 能力，禁止其他可登录角色继承/切入候选或旧写入角色。新候选使用独立、非超级用户、无创建角色/数据库/复制权限的 DB 登录，仅有本应用迁移和业务所需权限。

用真实 `pg_hba.conf`/防火墙或安全组限制数据库写入入口：候选独占的容器网络只接候选和主库，按候选固定地址的 `/32`、数据库名及专用登录放行，不对整个宿主子网或旧容器网络开放；禁止其他机器、旧容器及无关进程建立候选写入会话。新凭据只装配给受控候选，不共享旧服务；锁源、部署管理和密钥文件的宿主权限仅授予发布管理员，其他容器不能挂载或读取它们。**仅凭“换了密码”或 receipt 文字不能验收**。记录实际网络成员、ACL、角色成员关系、活动会话和已停用任务的脱敏证据。备份/管理超级用户仅限受控维护窗口，不能与试运行业务并行修改钱包。不同角色/不同主机的非合作写入不受本机 flock 约束。

PG 必须直连已审核主库，`fsync=on`、`synchronous_commit=on/remote_apply`；数据库身份、版本、池上限、专用/退役角色与已审核 receipt 一致。保留原有 cutoff 期初余额、差异、真实在途、备份、访问控制证据；不逐笔重算历史，不丢弃真实在途。receipt 中候选提交必须绑定最终干净提交，镜像构建写入相同 `MOWAN_FUNDING_BUILD_COMMIT`，使用不可变镜像身份。切换前独立核对所有实际约束，不能把本地测试当作线上约束证据。

## iDataRiver 旧配置迁移契约

旧库只有 `IdataRiverApiKey`、`IdataRiverEnabled`、`IdataRiverProducts` 不足以启用旧单续付。新代码不恢复旧生产默认身份。管理员从已核实的旧部署配置及平台后台确认下列持久化 options：

| 键 | 验收条件 |
| --- | --- |
| `IdataRiverApiKey` | 原商户有效密钥通过受控配置渠道保留/填写；不出现在聊天、命令参数或报告中。空值更新保留原密钥。 |
| `IdataRiverMerchantId` | 平台确认的商户 ID，24 位小写十六进制，与既有订单归属一致。 |
| `IdataRiverProjectId` | 平台确认的项目 ID，24 位小写十六进制，与既有订单归属一致。 |
| `IdataRiverCallbackOrigin` | 实际网关 HTTPS 域名 origin；无路径、端口、query、fragment、userinfo，不用 IP/localhost。 |
| `IdataRiverProducts` | 非空有效 USD 套餐数组；每项 `sku_id/name/price/quota/currency` 合法，与平台现售配置一致，禁止测试 SKU；不改写旧订单保存的价格和额度快照。 |
| `IdataRiverEnabled` | 补齐并核对后显式设为 `true`；修改过程中保持 `false`。 |

通过已有管理员设置入口持久化，先禁用，再补齐身份/回调 origin/套餐，最后启用；可用现有批量更新事务一起提交完整配置。不在数据库里补造合规确认。根管理员使用真实控制台登录会话阅读并确认当前合规条款（现为 `v1`）；现有 `POST /api/option/payment_compliance` 记录本人、时间和 IP，PAT 不允许，配置迁移不代签。旧合规记录只有真实有效且版本符合时才能复用。

验收：重启重载 options 后仍完整；未填身份、禁用、未确认或条款过期时不开放续付；完整配置和有效确认时旧订单可查、平台仍 NEW 且支持支付时才可续付。旧通知 `POST /api/user/idatariver/notify` 和新通知均保持 HTTPS 到达，返回 `/wallet?trade_no=...`；平台服务端详情仍是金额/状态依据，历史订单按原归属和快照结算。保存脱敏配置存在性、真实回调连通与一次受控支付/退款正确性证据后才开放试运行。

## 本机必要检查

Windows：`GOWORK=off go test . -run TestFundingPilot -count=1`；Linux 编译使用 `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build .`。

Linux 宿主锁测试只可在**本机隔离、没有真实锁的私有 root 挂载命名空间**执行：先编译 `go test -c -o pilot-host-lock.test mowan_pilot_host_linux.go mowan_pilot_host_linux_test.go`，再 `unshare -m --propagation private env TEST_PILOT_HOST_NAMESPACE=1 ./pilot-host-lock.test -test.v -test.timeout=30s`。检查缺失挂载、可写挂载/源权限、跨挂载命名空间竞争、正常退出与强杀后接替。此测试不访问数据库、不代替部署验收、不做压测。
