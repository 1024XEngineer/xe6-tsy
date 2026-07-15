# 模块 3 前端接口文档

## 1. 基本信息

| 项目 | 内容 |
| --- | --- |
| 模块 | 接待会话与媒体接入 |
| 模块负责人 | 模块 3 负责人（待项目维护者填写姓名） |
| 使用页面 | 接待工作台的创建、开始、媒体状态、结束与取消操作 |
| 接口版本 | v1 |
| 统一入口 | Gin HTTP `/api/v1/reception` |
| 状态 | MS1 Fake Media 可联调 |

## 2. 调用方式

所有请求和响应使用 JSON，JSON 字段为 `snake_case`。写请求必须携带
`access_context_ref`、`idempotency_key` 和当前对象的 `expected_version`；创建请求固定传入
`trial-org`、`service-point-001`、`window-001`、`config-v1` 的 Demo 引用。

| 用途 | 方法与路径 | 成功状态 |
| --- | --- | --- |
| 创建会话 | `POST /api/v1/reception/sessions` | 201 |
| 查询会话 | `GET /api/v1/reception/sessions/{session_id}` | 200 |
| 启动会话 | `POST /api/v1/reception/sessions/{session_id}/start` | 200 |
| 接入 Fake Media | `POST /api/v1/reception/sessions/{session_id}/media-tracks` | 201 |
| 主动断开 | `POST /api/v1/reception/sessions/{session_id}/media-tracks/{binding_id}/detach` | 200 |
| 结束会话 | `POST /api/v1/reception/sessions/{session_id}/end` | 200 |
| 取消会话 | `POST /api/v1/reception/sessions/{session_id}/cancel` | 200 |

完整字段、枚举和响应结构以 `contracts/openapi/reception-v1.yaml` 为准。

## 3. 请求与响应示例

创建会话：

```json
{
  "idempotency_key": "demo-create-001",
  "access_context_ref": "access-demo",
  "organization_id": "trial-org",
  "service_point_id": "service-point-001",
  "service_window_id": "window-001",
  "organization_config_version": "config-v1",
  "processing_context_ref": "processing-demo"
}
```

启动后响应中的 `media_capability` 明确说明能否接入音频和人工文本是否可用。查询响应只返回
会话必要字段、配置/ProcessingContext 引用、媒体绑定和当前能力，不返回音频、群众正文或
其他模块可写数据。

## 4. 错误处理

错误统一为 `error.code/message/retryable/details + trace_id`。前端处理建议：

| 错误 | 前端行为 |
| --- | --- |
| `VALIDATION_FAILED` | 标出输入错误，不自动重试 |
| `ACCESS_*` / `ORGANIZATION_SCOPE_MISMATCH` | 阻止操作并要求刷新访问上下文 |
| `VERSION_MISMATCH` | 刷新会话后由工作人员重试 |
| `IDEMPOTENCY_CONFLICT` | 生成新键前先确认用户是否改变了请求 |
| `MEDIA_ATTACH_FAILED` | 保持接待页活动，切换到人工文本 |
| `MEDIA_DETACH_FAILED` | 不显示“已断开”，允许工作人员重试 |

## 5. 权限、幂等与版本

后端对每个写操作重新授权，前端不得仅凭按钮可见性判断权限。同一操作、相同幂等键和相同
请求会重放第一次结果，不重复生成实体或事件；同一键对应不同请求返回冲突。每次状态变更后
使用响应中的新 `version`，不得自行递增或静默覆盖。

## 6. 人工降级

`attach_failure` 或运行中断线时，绑定进入 `failed`，会话仍为 `active`。响应包含：

```json
{
  "degradation": {
    "mode": "manual_text",
    "session_remains_active": true,
    "reason_code": "MEDIA_ATTACH_FAILED"
  }
}
```

`realtime_audio_allowed=false` 也不阻止会话启动；前端应直接展示人工文本入口。

## 7. 仍需前端确认

- 幂等键由页面操作层还是统一 API client 生成。
- 媒体断线提示、重试按钮和人工文本区的最终交互文案。
- 会话结束前存在活动媒体绑定时的二次确认方式。
- `runtime_disconnect` 后新绑定的 UI 历史展示规则。
