# 产业考察合作线索归口

交流周结束后，台商、台青、区县招商人员可能就同一企业同一次走访各交一份口径冲突的合作意向。本服务把参访行程、企业授权、项目条件、园区反馈关联到**同一条合作意向**，并提供：

- **线索归口**：同一 `(enterprise_ref, visit_ref)` 的多来源记录合并为一条意向，冲突字段自动标记、留原始记录待人工研判；
- **企业授权与范围调整**：授权按 `consent_ref` 版本留痕，未公开前企业可调整合作范围，公开后锁定；
- **园区隔离**：经办人只能处理所属园区材料（请求头 `X-Operator-ID`）；
- **跨园转交回执**：跨区须发起转交，接收园区凭 `receipt_no` 接收/拒收，接收人与时间留痕；
- **政策版本门槛**：签约/公开前校验企业确认的政策版本仍是当前生效版本，版本变化须重新确认；
- **对外清单保护**：未公开意向与未获 `public_consent` 的联系人不会出现在对外清单；
- **全程追溯**：时间线从项目状态串起每次实地对接与分歧处理。

本服务采用 HTTP 接口和 SQLite 本地文件（CGO 驱动）。运行参数 `PORT` 指定监听端口，`DATABASE_PATH` 指定数据文件（缺省使用内存库）；建表脚本内嵌于二进制，启动时自动迁移。

## 本地开发

```sh
make test     # 运行自动化检查
make run      # 启动服务（默认 :8080，内存/文件库由 DATABASE_PATH 决定）
make migrate  # 可选：用 sqlite3 命令行手动应用 migrations/*.sql
make vet      # 静态检查
```

`docker compose up --build` 启动隔离容器（数据挂在 `app-data` 卷），`APP_PORT` 可调整宿主机端口。

## 快速上手

经办人接口都需要 `X-Operator-ID` 头。先登记基础目录：

```sh
curl -s -XPOST localhost:8080/admin/parks -d '{"park_ref":"PARK-A","name":"沈阳A园"}'
curl -s -XPOST localhost:8080/admin/enterprises -d '{"enterprise_ref":"ORG-1","name":"某台企"}'
curl -s -XPOST localhost:8080/admin/operators -d '{"operator_id":"OP-A","park_ref":"PARK-A","name":"甲"}'
curl -s -XPOST localhost:8080/admin/policy-revisions -d '{"version":"POL-V1","title":"首版优惠"}'
```

提交三方来源线索（同一企业+走访自动归口，冲突进 `conflicts`）：

```sh
curl -s -XPOST localhost:8080/v1/sources/enterprise -H 'X-Operator-ID: OP-A' -d '{
  "source_record_ref":"E-1","enterprise_ref":"ORG-1","visit_ref":"V-1",
  "payload":{"title":"落地项目","conditions":{"land":"50亩"}}}'
curl -s -XPOST localhost:8080/v1/sources/youth -H 'X-Operator-ID: OP-A' -d '{
  "source_record_ref":"Y-1","enterprise_ref":"ORG-1","visit_ref":"V-1",
  "payload":{"conditions":{"land":"90亩"}}}'
```

授权（逐人标记公开意愿）→ 确认政策 → 公开 → 签约：

```sh
curl -s -XPOST localhost:8080/v1/authorizations -H 'X-Operator-ID: OP-A' -d '{
  "consent_ref":"C-1","enterprise_ref":"ORG-1",
  "contacts":[{"name":"王经理","public_consent":true},{"name":"李助理","public_consent":false}]}'
curl -s -XPOST localhost:8080/v1/intents/INTENT-xxxx/policy-confirmations -H 'X-Operator-ID: OP-A'
curl -s -XPOST localhost:8080/v1/intents/INTENT-xxxx/publish -H 'X-Operator-ID: OP-A'
curl -s -XPOST localhost:8080/v1/intents/INTENT-xxxx/sign -H 'X-Operator-ID: OP-A'
```

跨园转交与回执：

```sh
curl -s -XPOST localhost:8080/v1/intents/INTENT-xxxx/transfers -H 'X-Operator-ID: OP-A' \
  -d '{"to_park_ref":"PARK-B"}'                          # 返回 receipt_no
curl -s -XPOST localhost:8080/v1/transfers/RC-xxxx/acknowledge -H 'X-Operator-ID: OP-B' \
  -d '{"accept":true}'
```

全程追溯与对外清单：

```sh
curl -s localhost:8080/v1/intents/INTENT-xxxx/timeline -H 'X-Operator-ID: OP-B'
curl -s localhost:8080/v1/public/intents                 # 无需鉴权；只含公开意向与同意联系人
```

完整字段、状态枚举与事件类型见 `contracts/entities.json`，领域规则见 `docs/domain.md`。`fixtures/example.json` 为不含真实身份的交换示例。
