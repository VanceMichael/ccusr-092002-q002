# 产业考察合作线索归口

参访记录、招商政策及企业授权分别由园区、招商主管部门与来访企业保存，跨区联系需保留受理方信息。

本服务采用 HTTP 接口和 SQLite 本地文件（通过 cgo 绑定系统 libsqlite3，不依赖外部 Go 模块）。运行参数 `PORT` 指定监听端口，`DATABASE_PATH` 指定数据文件；`fixtures/example.json` 保存不含真实身份的交换示例，`contracts/entities.json` 记录字段约定，`docs/domain.md` 介绍来源、范围与归口规则。

## 本地开发

`make migrate` 初始化数据文件（服务启动时也会自动迁移），`make test` 运行现有自动化检查，`make run` 启动服务。`docker compose up --build` 可以启动隔离容器，`APP_PORT` 可调整宿主机端口。

## 接口概览

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/visits` | 登记参访行程 |
| POST | `/intents` | 提交来源记录；同企业同走访自动归口并生成冲突分歧 |
| GET | `/intents/{id}` | 查看意向与联系人（内部视图） |
| PATCH | `/intents/{id}/scope` | 企业在公开前调整合作范围 |
| POST | `/intents/{id}/publish` | 所属园区经办人公开意向 |
| POST | `/intents/{id}/feedback` | 所属园区经办人记录反馈 |
| POST | `/intents/{id}/contacts` | 登记联系人（默认未授权） |
| POST | `/intents/{id}/contacts/{contactId}/consent` | 企业凭 `consent_ref` 授权联系人 |
| POST | `/intents/{id}/engagements` | 记录一次实地对接 |
| POST | `/intents/{id}/disputes/{disputeId}/resolve` | 处理分歧 |
| POST | `/intents/{id}/transfers` | 发起跨区转交 |
| POST | `/transfers/{id}/accept` | 接收园区确认并留下回执 |
| POST | `/parks/{park}/policies` | 登记园区优惠政策版本 |
| POST | `/intents/{id}/policy-reconfirmations` | 政策版本变化后重新确认 |
| POST | `/intents/{id}/sign` | 签约（校验分歧与政策版本） |
| GET | `/intents/{id}/trace` | 从项目状态追溯全部留痕 |
| GET | `/public/cooperations` | 对外合作清单（仅已公开，过滤未授权联系人） |

身份通过请求头声明：`X-Actor-Kind`（`handler` / `enterprise`）、`X-Actor-Id`、`X-Actor-Park`（经办人所属园区）、`X-Actor-Enterprise`（企业编号）。
