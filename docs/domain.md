# 领域资料

参访记录、招商政策及企业授权分别由园区、招商主管部门与来访企业保存，跨区联系需保留受理方信息。交流周结束后，台商、台青、区县招商人员三方可能就**同一企业的同一次走访**各交一份口径不一的合作意向，本服务把它们归口为同一条线索并全程留痕。

## 归口模型

- **意向（intent）** 是核心对象，唯一归口键为 `(enterprise_ref, visit_ref)`。三方提交的 `source_record`（来源取值 `enterprise`/`youth`/`district`）全部挂在同一意向下，不另立项目。
- 每条新到来源记录与归口视图比对 `title`、`conditions`、`park_ref`：不一致字段写入该记录的 `conflicts` 并把意向置为 `has_conflicts=true`，原始记录始终保留，由经办人研判后置平（`CONFLICTS_REVIEWED`）。
- 参访事实（visit）与归口意向分开：行程记录园区、发生时间与备注；意向记录项目条件、状态与归属。

## 企业授权与合作范围

- 企业授权按 `consent_ref` 版本保存（`authorizations`），联系人逐人标记 `public_consent`。
- 企业可在意向**未公开前**通过新版本授权调整合作范围（`SCOPE_ADJUSTED`），历史版本留痕；意向一旦 `published_at` 非空，范围与项目条件即锁定。
- 范围调整会清空原政策确认版本，企业须按新意愿重新确认政策。

## 园区隔离与跨园转交

- 经办人（operator）归属唯一园区（`operators.park_ref`）。所有意向读写都校验 `intents.current_park_ref` 与经办人园区一致，否则返回 403。
- 跨园必须发起转交（`transfers`），系统生成 `receipt_no` 回执。`PENDING` 期间归属不变；接收园区经办人凭回执接收（`ACCEPTED`，归属转移）或拒收（`REJECTED`，留存原因），接收人、接收时间全部记录。

## 政策版本门槛

- 优惠政策按 `policy_revisions.version` 管理，企业逐版本确认（`policy_confirmations`，随 `consent_ref` 留痕）。
- 签约（SIGNED）与对外公开前，要求**当前生效版本必须等于企业确认版本**；政策版本变化后返回 `ErrPolicyChanged`，企业重新确认前不能签约。

## 对外清单

`GET /v1/public/intents` 无需鉴权，但只输出：

1. `published_at` 非空的意向；
2. 其挂载授权版本中 `public_consent=true` 的联系人——未获企业同意的联系人绝不出现。

## 全程追溯

每条意向有一条追加式事件链（`intent_events`），时间线（`GET /v1/intents/{id}/timeline`）把来源记录、授权版本、政策确认、转交回执、园区反馈、每次实地对接（engagements）及挂在对接上的分歧处理（disputes，含处理结论与处理人）与状态流转串联，可从项目状态一直追到每次实地对接和分歧处理。

## 数据约定

`contracts/entities.json` 保存实体字段、状态枚举、事件类型与接口清单。来源编号仅表示提交方对同一记录的识别符；示例文件（`fixtures/example.json`）全部为虚构资料，不含真实个人数据。运行时持久化文件由 `DATABASE_PATH` 决定（缺省为内存库）；时间字段统一使用带时区偏移的 ISO 8601 字符串。建表脚本内嵌于二进制（`migrations/*.sql`），服务启动时按文件名顺序自动应用并登记到 `schema_migrations`。
