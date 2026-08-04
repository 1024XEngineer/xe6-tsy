# Lingow 数据设计文档

## 1. 范围

本文档整理仓库当前实现的数据设计范围。

当前数据设计覆盖：账户、认证、语音会话、语言配置、说话人、Final Turn、用量记录、异步消息投递、Redis/Valkey 队列。

音频流、PCM 数据、WebRTC 临时事件、VAD partial、ASR partial、TTS 播放运行态不落业务 PostgreSQL。

## 2. 总体关系

```text
lingow_accounts
    ├── lingow_auth_sessions
    ├── email_bind_challenges
    ├── account_destinations
    ├── message_preferences
    ├── outbound_messages
    │   └── delivery_attempts
    │       └── delivery_outbox
    ├── delivery_retry_requests
    └── voice_sessions
        ├── voice_session_create_requests
        ├── voice_session_start_operations
        ├── voice_session_end_intents
        ├── voice_session_language_configs
        ├── voice_session_participants
        │   └── voice_turns
        │       └── attribution_tasks
        └── lingow_usage_records

final_turn_outbox 独立承载 Final Turn 入站事件收据状态。
lingow_phone_challenges 独立承载注册前手机号 OTP 挑战，不通过 `account_id` 关联账户。
supported_languages 独立承载语言字典。
```

## 3. 设计原则

- `services/api` 拥有账户、会话、语言配置、Final Turn 持久化、历史查询、用量汇总和异步消息投递；Final Turn 的 PostgreSQL consumer 已实现。
- `services/realtime-audio` 拥有 WebRTC 音频、VAD、ASR、翻译、TTS、打断和运行时状态机，并通过当前配置的 outbox 发布实时事件。
- PostgreSQL 保存长期业务事实；Redis/Valkey 保存队列、消费组、延迟重试调度和实时事件幂等状态。
- Final Turn 与 UsageFact 都按事件幂等写入，避免实时链路重复投递造成重复计费或重复历史。
- `voice_turns` 的文本、语言、时间、序号等事实字段写入后不可变；说话人归属允许通过 `participant_id`、`speaker_confidence`、`attribution_status`、`corrected_by`、`corrected_at` 修正，说话人快照字段随归属修正同步为目标 participant 的当前值。
- `lingow_usage_records` 完全不可更新；用量汇总通过实时聚合查询生成，仓库代码当前没有物化的 `voice_session_usage_summaries` 表。
- `request_hash` 和 `request_fingerprint` 由应用层根据 canonical request 计算，用于同一幂等键重放时确认请求内容一致，不是业务对象的内容副本。

本章的语音会话、语言配置、说话人、Final Turn 和用量部分是核心语音翻译数据；账户、消息目标和异步消息投递部分是仓库已经实现的控制面扩展，不代表每个语音会话都必须启用外部消息投递。

## 4. PostgreSQL 表结构

### 4.1 `lingow_accounts`

账户主表，支持匿名账户、手机号注册账户和匿名账户合并到注册账户后的 lineage 查询。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | `TEXT` | 否 |  | 账户 ID |
| `kind` | `TEXT` | 否 |  | `anonymous` / `registered` |
| `phone_hash` | `TEXT` | 是 |  | 旧版手机号摘要，保留用于历史兼容与懒升级 |
| `merged_into` | `TEXT` | 是 |  | 被合并到的账户 ID |
| `created_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 创建时间 |
| `phone_hash_v2` | `TEXT` | 是 |  | HMAC pepper 后的手机号摘要 |

索引与约束：

| 名称 | 类型 | 定义 |
| --- | --- | --- |
| `lingow_accounts_pkey` | PK | `PRIMARY KEY (id)` |
| `lingow_accounts_merged_into_fkey` | FK | `merged_into REFERENCES lingow_accounts(id) ON DELETE RESTRICT` |
| `lingow_accounts_phone_hash_key` | Unique Partial Index | `(phone_hash) WHERE phone_hash IS NOT NULL` |
| `lingow_accounts_phone_hash_v2_key` | Unique Partial Index | `(phone_hash_v2) WHERE phone_hash_v2 IS NOT NULL` |
| `accounts_id_not_empty` | CHECK | `id <> ''` |
| `accounts_kind_valid` | CHECK | `kind IN ('anonymous', 'registered')` |
| `accounts_identity_valid` | CHECK | 匿名账户无手机号摘要；注册账户至少有一个手机号摘要且不能再被 merge |

### 4.2 `lingow_phone_challenges`

手机号 OTP 挑战表，保存验证码摘要、过期时间、尝试次数和摘要版本。
该表不通过 `account_id` 关联账户；它按手机号摘要保存注册前的 OTP 挑战，生命周期不依赖某个已存在的 `lingow_accounts` 记录。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | `TEXT` | 否 |  | 挑战 ID |
| `phone_hash` | `TEXT` | 否 |  | 当前手机号摘要 |
| `code_hash` | `TEXT` | 否 |  | OTP 摘要 |
| `expires_at` | `TIMESTAMPTZ` | 否 |  | 过期时间 |
| `used_at` | `TIMESTAMPTZ` | 是 |  | 使用时间 |
| `created_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 创建时间 |
| `attempts` | `SMALLINT` | 否 | `0` | 已尝试次数 |
| `max_attempts` | `SMALLINT` | 否 | `5` | 最大尝试次数 |
| `last_attempt_at` | `TIMESTAMPTZ` | 是 |  | 最近尝试时间 |
| `legacy_phone_hash` | `TEXT` | 否 |  | 旧摘要兼容值 |
| `digest_version` | `SMALLINT` | 否 | `1` | 摘要版本，`1` / `2` |

索引与约束：

| 名称 | 类型 | 定义 |
| --- | --- | --- |
| `lingow_phone_challenges_pkey` | PK | `PRIMARY KEY (id)` |
| `lingow_phone_challenges_phone_expiry_idx` | Index | `(phone_hash, expires_at DESC)` |
| `lingow_phone_challenges_phone_created_idx` | Index | `(phone_hash, created_at DESC)` |
| `lingow_phone_challenges_legacy_phone_created_idx` | Index | `(legacy_phone_hash, created_at DESC)` |
| `lingow_phone_challenges_attempts_valid` | CHECK | `attempts >= 0 AND attempts <= max_attempts` |
| `lingow_phone_challenges_max_attempts_valid` | CHECK | `max_attempts BETWEEN 1 AND 10` |
| `lingow_phone_challenges_digest_version_valid` | CHECK | `digest_version IN (1, 2)` |
| `lingow_phone_challenges_expiry_valid` | CHECK | `expires_at > created_at` |
| `lingow_phone_challenges_used_at_valid` | CHECK | `used_at IS NULL OR used_at >= created_at` |
| `lingow_phone_challenges_last_attempt_valid` | CHECK | `last_attempt_at IS NULL OR last_attempt_at >= created_at` |

### 4.2.1 `email_bind_challenges`

短期邮箱所有权验证表。验证成功后，应用才会将邮箱目标写入或更新到 `account_destinations`；该表不是语音翻译核心表。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | `TEXT` | 否 |  | 验证挑战 ID |
| `account_id` | `TEXT` | 否 |  | 账户 ID |
| `destination_ref` | `TEXT` | 否 |  | 目标引用 |
| `email` | `TEXT` | 否 |  | 待验证邮箱 |
| `token_hash` | `TEXT` | 否 |  | 一次性 token 摘要 |
| `expires_at` | `TIMESTAMPTZ` | 否 |  | 过期时间 |
| `used_at` | `TIMESTAMPTZ` | 是 |  | 消费时间 |
| `created_at` | `TIMESTAMPTZ` | 否 | `NOW()` | 创建时间 |

索引与约束：

| 名称 | 类型 | 定义 |
| --- | --- | --- |
| `email_bind_challenges_pkey` | PK | `PRIMARY KEY (id)` |
| `email_bind_challenges_token_hash_key` | Unique Index | `(token_hash)` |
| `email_bind_challenges_account_active_idx` | Partial Index | `(account_id, created_at DESC) WHERE used_at IS NULL` |
| `email_bind_challenges_expiry_valid` | CHECK | `expires_at > created_at` |
| `email_bind_challenges_used_at_valid` | CHECK | `used_at IS NULL OR used_at >= created_at` |

### 4.3 `lingow_auth_sessions`

登录刷新会话表。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | `TEXT` | 否 |  | 登录会话 ID |
| `account_id` | `TEXT` | 否 |  | 账户 ID |
| `refresh_hash` | `TEXT` | 否 |  | Refresh Token 摘要 |
| `expires_at` | `TIMESTAMPTZ` | 否 |  | 过期时间 |
| `revoked_at` | `TIMESTAMPTZ` | 是 |  | 撤销时间 |
| `created_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 创建时间 |

索引与约束：

| 名称 | 类型 | 定义 |
| --- | --- | --- |
| `lingow_auth_sessions_pkey` | PK | `PRIMARY KEY (id)` |
| `lingow_auth_sessions_account_id_fkey` | FK | `account_id REFERENCES lingow_accounts(id) ON DELETE RESTRICT` |
| `lingow_auth_sessions_refresh_hash_key` | Unique Index | `(refresh_hash)` |
| `lingow_auth_sessions_active_refresh_lookup_idx` | Partial Index | `(refresh_hash, expires_at) WHERE revoked_at IS NULL` |
| `lingow_auth_sessions_expiry_valid` | CHECK | `expires_at > created_at` |
| `lingow_auth_sessions_revoked_at_valid` | CHECK | `revoked_at IS NULL OR revoked_at >= created_at` |

### 4.4 `voice_sessions`

业务语音会话主表。运行时播放、听音、识别等状态不在该表维护。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | `TEXT` | 否 |  | 会话 ID |
| `account_id` | `TEXT` | 否 |  | 归属账户 |
| `status` | `TEXT` | 否 |  | `created` / `active` / `ended` / `failed` |
| `audio_config` | `JSONB` | 否 |  | 音频格式与采样配置 |
| `capabilities` | `JSONB` | 否 |  | 终端能力 |
| `failure_error_code` | `TEXT` | 是 |  | 失败原因码 |
| `started_at` | `TIMESTAMPTZ` | 是 |  | 开始时间 |
| `ended_at` | `TIMESTAMPTZ` | 是 |  | 结束或失败终态时间 |
| `created_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 创建时间 |

索引与约束：

| 名称 | 类型 | 定义 |
| --- | --- | --- |
| `voice_sessions_pkey` | PK | `PRIMARY KEY (id)` |
| `voice_sessions_account_id_fkey` | FK | `account_id REFERENCES lingow_accounts(id) ON DELETE RESTRICT` |
| `voice_sessions_id_account_id_key` | UNIQUE | `(id, account_id)`，供账户范围 FK 使用 |
| `voice_sessions_account_created_order_idx` | Index | `(account_id, created_at DESC, id DESC)` |
| `voice_sessions_account_status_created_order_idx` | Index | `(account_id, status, created_at DESC, id DESC)` |
| `voice_sessions_status_valid` | CHECK | `status IN ('created', 'active', 'ended', 'failed')` |
| `voice_sessions_config_objects` | CHECK | `audio_config` 与 `capabilities` 必须为 JSON object |
| `voice_sessions_timestamps_valid` | CHECK | 状态与 `started_at`、`ended_at`、`failure_error_code` 的组合必须合法 |

当前契约中的 `capabilities` 不是开放扩展字段，只允许并要求以下布尔能力：`webrtc`、`data_channel`、`microphone`、`speaker`、`speaker_diarization`。数据库用 `JSONB` 保存契约对象并只校验 object 形态，具体字段合法性由 OpenAPI 契约和 `services/api/sessions` 服务层负责。

### 4.5 `voice_session_create_requests`

会话创建幂等请求表。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `account_id` | `TEXT` | 否 |  | 账户 ID |
| `idempotency_key` | `TEXT` | 否 |  | 幂等键 |
| `request_hash` | `TEXT` | 否 |  | 请求体指纹 |
| `session_id` | `TEXT` | 否 |  | 创建出的会话 ID |
| `created_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 创建时间 |

索引与约束：

| 名称 | 类型 | 定义 |
| --- | --- | --- |
| `voice_session_create_requests_pkey` | PK | `PRIMARY KEY (account_id, idempotency_key)` |
| `voice_session_create_requests_session_key` | FK | `(session_id, account_id) REFERENCES voice_sessions(id, account_id)` |

### 4.6 `voice_session_start_operations`

会话启动操作表。它是跨实例启动、补偿 Stop 和幂等重放的权威状态表。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `operation_id` | `TEXT` | 否 |  | 操作 ID |
| `session_id` | `TEXT` | 否 |  | 会话 ID |
| `account_id` | `TEXT` | 否 |  | 账户 ID |
| `idempotency_key` | `TEXT` | 否 |  | 幂等键 |
| `request_hash` | `TEXT` | 否 |  | 请求体指纹 |
| `status` | `TEXT` | 否 |  | `pending` / `compensating` / `completed` / `compensated` / `compensation_failed` |
| `compensation_claim_id` | `TEXT` | 是 |  | 补偿任务领取 ID |
| `started_at` | `TIMESTAMPTZ` | 是 |  | 实际启动成功时间 |
| `created_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 创建时间 |
| `updated_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 更新时间 |

索引与约束：

| 名称 | 类型 | 定义 |
| --- | --- | --- |
| `voice_session_start_operations_pkey` | PK | `PRIMARY KEY (operation_id)` |
| `voice_session_start_operations_key_unique` | UNIQUE | `(account_id, idempotency_key)` |
| `voice_session_start_operations_session_key` | FK | `(session_id, account_id) REFERENCES voice_sessions(id, account_id)` |
| `voice_session_start_operations_one_unfinished_per_session` | Unique Partial Index | `(session_id) WHERE status IN ('pending', 'compensating', 'compensation_failed')` |
| `voice_session_start_operations_account_session_key_idx` | Index | `(account_id, session_id, idempotency_key)` |

### 4.7 `voice_session_end_intents`

会话结束意图表，用于 Stop 失败后的恢复重试。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `session_id` | `TEXT` | 否 |  | 会话 ID |
| `account_id` | `TEXT` | 否 |  | 账户 ID |
| `reason` | `TEXT` | 否 |  | `user_requested` / `operator_cancelled` / `client_disconnected` |
| `idempotency_key` | `TEXT` | 否 |  | 幂等键 |
| `request_hash` | `TEXT` | 否 |  | 请求体指纹 |
| `requested_at` | `TIMESTAMPTZ` | 否 |  | 请求结束时间 |
| `completed_at` | `TIMESTAMPTZ` | 是 |  | 结束完成时间 |
| `trace_id` | `TEXT` | 否 |  | 恢复链路追踪 ID |
| `retry_count` | `INTEGER` | 否 | `0` | 恢复重试次数 |
| `last_error` | `TEXT` | 是 |  | 最近错误 |
| `next_attempt_at` | `TIMESTAMPTZ` | 否 |  | 下次恢复时间 |
| `recovery_owner` | `TEXT` | 是 |  | 恢复任务持有者 |
| `recovery_lease_expires_at` | `TIMESTAMPTZ` | 是 |  | 恢复租约过期时间 |

索引与约束：

| 名称 | 类型 | 定义 |
| --- | --- | --- |
| `voice_session_end_intents_pkey` | PK | `PRIMARY KEY (session_id, account_id)` |
| `voice_session_end_intents_key_unique` | UNIQUE | `(account_id, idempotency_key)` |
| `voice_session_end_intents_session_key` | FK | `(session_id, account_id) REFERENCES voice_sessions(id, account_id)` |
| `voice_session_end_intents_recovery_due_idx` | Partial Index | `(next_attempt_at, requested_at, session_id) WHERE completed_at IS NULL` |

### 4.8 `supported_languages`

可选语言字典。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `language_code` | `VARCHAR(10)` | 否 |  | 语言代码，如 `zh-CN`、`en-US` |
| `display_name` | `VARCHAR(64)` | 否 |  | 中文展示名 |
| `display_name_en` | `VARCHAR(64)` | 否 | `''` | 英文展示名 |
| `supports_as_source` | `BOOLEAN` | 否 | `TRUE` | 是否支持作为源语言 |
| `supports_as_target` | `BOOLEAN` | 否 | `TRUE` | 是否支持作为目标语言 |
| `sort_order` | `INT` | 否 | `0` | 排序 |
| `is_active` | `BOOLEAN` | 否 | `TRUE` | 是否启用 |

### 4.9 `voice_session_language_configs`

会话语言配置表。注意：issue #76 后续评论提出的简化版 `language_1_code` / `language_2_code` 尚未成为当前仓库代码；仓库当前实现仍是版本化 `language_pairs JSONB`。

版本、状态、生效区间和请求指纹共同支持并发更新、幂等重放和历史查询，因此比“每个会话一行当前语言对”的 MVP 方案更重。若产品确定不需要配置历史和乐观并发控制，可将其视为后续简化候选；本文档按当前可运行代码记录。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | `VARCHAR(26)` | 否 |  | 配置 ID |
| `session_id` | `TEXT` | 否 |  | 会话 ID，当前无 FK |
| `version` | `INT` | 否 |  | 会话内配置版本 |
| `language_pairs` | `JSONB` | 否 |  | 语言对配置 |
| `status` | `VARCHAR(20)` | 否 |  | `active` / `superseded` / `expired` |
| `effective_from` | `TIMESTAMPTZ` | 否 |  | 生效时间 |
| `effective_until` | `TIMESTAMPTZ` | 是 |  | 失效时间 |
| `created_by` | `VARCHAR(64)` | 否 |  | 创建来源 |
| `idempotency_key` | `VARCHAR(128)` | 是 |  | 幂等键 |
| `created_at` | `TIMESTAMPTZ` | 否 | `NOW()` | 创建时间 |
| `updated_at` | `TIMESTAMPTZ` | 否 | `NOW()` | 更新时间 |
| `request_fingerprint` | `VARCHAR(128)` | 是 |  | 完整请求指纹 |

索引与约束：

| 名称 | 类型 | 定义 |
| --- | --- | --- |
| `voice_session_language_configs_pkey` | PK | `PRIMARY KEY (id)` |
| `chk_lang_config_status` | CHECK | `status IN ('active', 'superseded', 'expired')` |
| `idx_lang_config_active` | Unique Partial Index | `(session_id, status) WHERE status = 'active'` |
| `idx_lang_config_version` | Unique Index | `(session_id, version)` |
| `idx_lang_config_idempotency` | Unique Partial Index | `(idempotency_key) WHERE idempotency_key IS NOT NULL` |
| `idx_lang_config_session_version_desc` | Index | `(session_id, version DESC)` |

### 4.10 `voice_session_participants`

会话内说话人表，负责稳定临时编号和可选身份引用。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | `TEXT` | 否 |  | 会话参与者 ID |
| `session_id` | `TEXT` | 否 |  | 所属会话 ID，当前不强制 FK 到 `voice_sessions` |
| `speaker_code` | `TEXT` | 否 |  | 会话内稳定编号，如 `speaker_01` |
| `display_name` | `TEXT` | 是 |  | 展示名称，如“说话人 A”或用户确认后的名称 |
| `provider_speaker_id` | `TEXT` | 是 |  | 供应商侧说话人聚类 ID |
| `voice_profile_id` | `TEXT` | 是 |  | 声纹档案引用，目前只是可选外部引用，仓库未建声纹档案表 |
| `confidence` | `DOUBLE PRECISION` | 是 |  | 当前身份置信度 |
| `created_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 首次识别时间 |
| `updated_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 最近修正时间 |

索引与约束：

| 名称 | 类型 | 定义 |
| --- | --- | --- |
| `voice_session_participants_pkey` | PK | `PRIMARY KEY (id)` |
| `voice_session_participants_session_speaker_code_key` | UNIQUE | `(session_id, speaker_code)` |
| `voice_session_participants_session_id_id_key` | UNIQUE | `(session_id, id)`，供 `voice_turns` 复合 FK 使用 |
| `voice_session_participants_session_provider_speaker_id_key` | Unique Partial Index | `(session_id, provider_speaker_id) WHERE provider_speaker_id IS NOT NULL` |
| `voice_session_participants_session_speaker_order_idx` | Index | `(session_id, speaker_code ASC, id ASC)` |

`session_id` 未设置到 `voice_sessions` 的 FK 是仓库为历史记录和异步 FinalTurn 写入保留的弱引用边界，不表示应用可以跨会话任意关联参与者。`voice_turns` 仍通过 `(session_id, participant_id)` 复合 FK 保证 Turn 与 Participant 属于同一会话。

`voice_profile_id` 当前只是可空外部引用，仓库没有声纹档案表，MVP 会话内临时说话人流程不依赖它。`provider_speaker_id` 用于复用供应商在单个会话内产生的说话人聚类；当前唯一索引隐含同一会话只使用一个 diarization provider 的假设。

### 4.11 `voice_turns`

Final Turn 持久化表，保存每轮原文、译文、语言方向和说话人归属状态。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | `TEXT` | 否 |  | Turn ID |
| `event_id` | `TEXT` | 否 |  | FinalTurn 事件 ID，唯一 |
| `event_payload_hash` | `BYTEA` | 否 |  | 事件 payload SHA-256，长度 32 字节 |
| `session_id` | `TEXT` | 否 |  | 会话 ID |
| `participant_id` | `TEXT` | 是 |  | 当前归属参与者，分离结果前可为空 |
| `speaker_code` | `TEXT` | 否 |  | 稳定说话人编号，归属修正时随目标 participant 刷新 |
| `display_name` | `TEXT` | 是 |  | 展示名称快照，归属修正时随目标 participant 刷新 |
| `provider_speaker_id` | `TEXT` | 是 |  | 供应商说话人 ID 快照，归属修正时随目标 participant 刷新 |
| `voice_profile_id` | `TEXT` | 是 |  | 声纹档案引用快照，归属修正时随目标 participant 刷新 |
| `sequence_no` | `BIGINT` | 否 |  | 会话内轮次序号，从 1 开始 |
| `source_language` | `TEXT` | 否 |  | 实际识别语言 |
| `target_language` | `TEXT` | 否 |  | 本轮目标语言，由实时转译模块按当前双语配置和识别语言确定 |
| `language_config_version` | `BIGINT` | 否 |  | 本轮使用的语言配置版本 |
| `source_text` | `TEXT` | 否 |  | 识别原文 |
| `translated_text` | `TEXT` | 否 |  | 翻译结果 |
| `speaker_confidence` | `DOUBLE PRECISION` | 是 |  | 本轮说话人判断置信度 |
| `attribution_status` | `TEXT` | 否 |  | `pending` / `provisional` / `confirmed` / `corrected` |
| `corrected_by` | `TEXT` | 是 |  | 当前仓库只允许 `system` |
| `started_at` | `TIMESTAMPTZ` | 否 |  | 发言开始时间 |
| `ended_at` | `TIMESTAMPTZ` | 否 |  | 发言结束时间 |
| `corrected_at` | `TIMESTAMPTZ` | 是 |  | 归属修正时间 |
| `created_at` | `TIMESTAMPTZ` | 否 |  | 创建时间 |

索引与约束：

| 名称 | 类型 | 定义 |
| --- | --- | --- |
| `voice_turns_pkey` | PK | `PRIMARY KEY (id)` |
| `voice_turns_event_id_key` | UNIQUE | `(event_id)` |
| `voice_turns_session_sequence_no_key` | UNIQUE | `(session_id, sequence_no)` |
| `voice_turns_session_participant_foreign_key` | FK | `(session_id, participant_id) REFERENCES voice_session_participants(session_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT` |
| `voice_turns_session_sequence_order_idx` | Index | `(session_id, sequence_no ASC, id ASC)` |
| `voice_turns_history_created_order_idx` | Index | `(created_at DESC, id DESC)` |
| `voice_turns_session_history_order_idx` | Index | `(session_id, created_at DESC, id DESC)` |
| `voice_turns_reject_immutable_updates` | Trigger | 禁止更新文本、语言方向、时间、序号和身份键等不可变字段；说话人快照字段允许随归属修正更新 |

Turn 侧的 `speaker_code`、`display_name`、`provider_speaker_id`、`voice_profile_id` 是归属快照：初始 FinalTurn 落库时与实时阶段识别结果一致，归属修正（`PATCH /api/v1/voice-turns/{id}/attribution`）时在同一 UPDATE 中跟随目标 participant 刷新，保证返回给调用方和渲染端的 `participant_id` 与说话人标签始终自洽。真正不可变的是转译快照（`source_text`、`translated_text`、语言方向、配置版本、时间、序号和身份键）。当前表只保存最终修正状态，不保存每一次修正历史。

`voice_turns.provider_speaker_id` 在初始 FinalTurn 落库时由实时链路写入：`FinalTurnEvent.provider_speaker_id` 只有在 ASR/diarization 提供会话内稳定的 cluster key 时才填充，缺失时保持 `NULL`。异步归属 worker 依据该字段建立 participant 稳定映射；没有该字段的 turn 无法确定性归属，其任务被永久标记失败（`no_provider_speaker_id`）而不是伪造成功。当前仓库的 Qwen ASR adapter 不产生 speaker key，因此默认只保留 pending。

`language_config_version` 是每个新 FinalTurn 的必填正整数。Realtime 在 Turn 开始时读取并固定语言配置快照版本，随后将该版本同时写入 FinalTurn event 和 `voice_turns`；API consumer 不推断、不补默认值。归属修正只更新 attribution 字段，不会修改语言配置版本，因此历史记录可以准确追溯当轮使用的双语配置。

### 4.12 `attribution_tasks`

异步说话人归属的持久化工作队列。当 FinalTurn 以 `pending` 或历史兼容的 `provisional` 状态落库时，在同一事务内为每个 turn 创建一条任务（`turn_id` 唯一）。当前 realtime 生产链路只产生 pending；provisional 仅用于历史数据和兼容 contract。API 的 attribution worker 领取任务、解析归属并结算。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `task_id` | `TEXT` | 否 |  | 任务 ID，格式 `attr_<turn_id>` |
| `turn_id` | `TEXT` | 否 |  | 目标 turn ID，唯一 |
| `session_id` | `TEXT` | 否 |  | 会话 ID |
| `account_id` | `TEXT` | 否 |  | 会话归属账户（合并链的当前 owner） |
| `task_type` | `TEXT` | 否 |  | 当前只使用 `turn_attribution` |
| `status` | `TEXT` | 否 | `pending` | `pending` / `processing` / `completed` / `failed` |
| `available_at` | `TIMESTAMPTZ` | 否 |  | 最早可领取时间，重试时按指数退避后移 |
| `receipt` | `TEXT` | 是 |  | 领取凭据，`processing` 时非空 |
| `locked_until` | `TIMESTAMPTZ` | 是 |  | lease 到期时间，过期后任务可被重新领取 |
| `attempts` | `INTEGER` | 否 | `0` | 已领取次数；超过上限后停止重试 |
| `last_error` | `TEXT` | 是 |  | 最后一次失败原因，永久失败记录稳定错误码 |
| `created_at` | `TIMESTAMPTZ` | 否 |  | 创建时间 |
| `updated_at` | `TIMESTAMPTZ` | 否 |  | 更新时间 |

处理语义：

- worker 按 `FOR UPDATE SKIP LOCKED` 领取到期任务，成功写入后 Ack，临时错误按指数退避 Retry，缺少 provider speaker key 或超过尝试上限时 Fail；没有 evidence 不会创建 participant。
- 任务只在实时 FinalTurn 写入时入队；上线前的历史 unresolved turn 由 migration `000017` 幂等回填，并把旧的 `completed` 但 turn 仍 unresolved 的任务修复为 `pending`。
- 没有 `provider_speaker_id` 的 turn 无法确定性归属，任务以 `no_provider_speaker_id` 永久失败，保证 unresolved 数据可审计。

### 4.13 `final_turn_outbox`

Final Turn 入站事件的 PostgreSQL 持久化表及消费状态表。当前仓库已实现 PostgreSQL sink 和 API consumer，但 realtime-audio 生产运行时默认使用内存 outbox；Valkey outbox 当前仅接受 `usage.recorded` 事件。因此该表目前是已实现但尚未接入默认 realtime 生产 composition 的持久化 schema，不应视为当前默认运行链路已经使用。`delivery_outbox` 负责 API 到外部消息渠道的出站投递，两者方向和幂等身份不同。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `event_id` | `TEXT` | 否 |  | FinalTurn 事件 ID |
| `turn_id` | `TEXT` | 否 |  | Turn ID |
| `session_id` | `TEXT` | 否 |  | 会话 ID |
| `sequence_no` | `BIGINT` | 否 |  | 会话内轮次序号 |
| `payload_hash` | `BYTEA` | 否 |  | payload SHA-256，长度 32 字节 |
| `payload` | `JSONB` | 否 |  | FinalTurn 事件 payload |
| `status` | `TEXT` | 否 | `'pending'` | `pending` / `processing` / `acked` / `rejected` |
| `available_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 可消费时间 |
| `receipt` | `TEXT` | 是 |  | 当前处理收据 |
| `locked_until` | `TIMESTAMPTZ` | 是 |  | 锁过期时间 |
| `attempts` | `INTEGER` | 否 | `0` | 尝试次数 |
| `last_error` | `TEXT` | 是 |  | 最近一次 Nack 或 Reject 的错误信息；用于重试诊断和毒消息审计 |
| `rejected_at` | `TIMESTAMPTZ` | 是 |  | 消息进入 `rejected` 状态的时间；仅永久拒绝或达到最大尝试次数时设置 |
| `created_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 创建时间 |

处理语义：

- worker 领取事件后，成功持久化则 Ack；临时错误 Nack 并记录 `last_error`，事件按延迟重新进入 `pending`。
- 非法事件、幂等冲突、引用对象不存在等永久错误直接 Reject；临时错误达到 8 次尝试后也 Reject，并记录 `last_error` 和 `rejected_at`。

索引与约束：

| 名称 | 类型 | 定义 |
| --- | --- | --- |
| `final_turn_outbox_pkey` | PK | `PRIMARY KEY (event_id)` |
| `final_turn_outbox_available_idx` | Partial Index | `(available_at ASC, created_at ASC, event_id ASC) WHERE status = 'pending'` |
| `final_turn_outbox_lease_idx` | Partial Index | `(locked_until ASC, created_at ASC, event_id ASC) WHERE status = 'processing'` |
| `final_turn_outbox_reject_payload_updates` | Trigger | 禁止更新 payload 身份与内容字段 |

### 4.14 `lingow_usage_records`

用量事实表。实时转译模块提交 UsageFact，API 幂等写入该表。会话和账户维度汇总由代码查询聚合，没有单独 summary 表。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `event_version` | `INTEGER` | 否 |  | 当前固定为 `1` |
| `event_id` | `TEXT` | 否 |  | 用量事件 ID |
| `trace_id` | `TEXT` | 否 |  | 链路追踪 ID |
| `idempotency_key` | `TEXT` | 否 |  | 幂等键，全局唯一 |
| `payload_hash` | `BYTEA` | 否 |  | payload SHA-256，长度 32 字节 |
| `account_id` | `TEXT` | 否 |  | 费用归属账户 |
| `session_id` | `TEXT` | 否 |  | 会话 ID |
| `turn_id` | `TEXT` | 否 |  | Turn ID |
| `service_type` | `TEXT` | 否 |  | `asr` / `translation` / `tts` / `diarization` |
| `provider` | `TEXT` | 否 |  | 服务商 |
| `model` | `TEXT` | 否 |  | 模型 |
| `input_tokens` | `BIGINT` | 否 |  | 输入 token 数 |
| `output_tokens` | `BIGINT` | 否 |  | 输出 token 数 |
| `audio_duration_ms` | `BIGINT` | 否 |  | 计费音频时长 |
| `cost_amount` | `NUMERIC(20, 8)` | 是 |  | 成本金额；为空表示供应商未返回价格 |
| `currency` | `TEXT` | 是 |  | 三位大写币种；与 `cost_amount` 成对为空或非空 |
| `occurred_at` | `TIMESTAMPTZ` | 否 |  | 事件发生时间 |
| `recorded_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 落库时间 |

索引与约束：

| 名称 | 类型 | 定义 |
| --- | --- | --- |
| `lingow_usage_records_pkey` | PK | `PRIMARY KEY (event_id)` |
| `lingow_usage_records_session_key` | FK | `(session_id, account_id) REFERENCES voice_sessions(id, account_id)` |
| `lingow_usage_records_idempotency_key` | Unique Index | `(idempotency_key)` |
| `lingow_usage_records_session_service_occurred_idx` | Index | `(session_id, service_type, occurred_at ASC, event_id ASC)` |
| `lingow_usage_records_account_occurred_idx` | Index | `(account_id, occurred_at ASC, event_id ASC)` |
| `lingow_usage_records_reject_updates` | Trigger | 禁止更新任何用量事实 |

### 4.15 `account_destinations`

账户消息投递目标表。当前仓库支持 `email` 和 `wechat`。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | `TEXT` | 否 |  | 目标 ID |
| `account_id` | `TEXT` | 否 |  | 账户 ID |
| `channel` | `TEXT` | 否 |  | `email` / `wechat` |
| `destination_ref` | `TEXT` | 否 |  | 目标引用，如邮箱摘要或外部账号引用 |
| `provider_target_ciphertext` | `BYTEA` | 否 |  | 加密后的实际投递目标 |
| `key_version` | `TEXT` | 否 |  | 加密密钥版本 |
| `verified_at` | `TIMESTAMPTZ` | 是 |  | 验证时间 |
| `revoked_at` | `TIMESTAMPTZ` | 是 |  | 撤销时间 |
| `created_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 创建时间 |
| `updated_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 更新时间 |

### 4.16 `message_preferences`

账户消息偏好表。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `account_id` | `TEXT` | 否 |  | 账户 ID |
| `channel` | `TEXT` | 否 |  | `email` / `wechat` |
| `enabled` | `BOOLEAN` | 否 |  | 是否启用 |
| `verified` | `BOOLEAN` | 否 | `FALSE` | 是否已验证 |
| `updated_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 更新时间 |

### 4.17 `outbound_messages`

异步消息快照表。它保存不可变的发送内容快照，状态和尝试次数可随投递推进更新。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | `TEXT` | 否 |  | 消息 ID |
| `account_id` | `TEXT` | 否 |  | 账户 ID |
| `idempotency_key` | `TEXT` | 否 |  | 账户内幂等键 |
| `channel` | `TEXT` | 否 |  | `email` / `wechat` |
| `destination_ref` | `TEXT` | 否 |  | 投递目标引用 |
| `snapshot_version` | `INTEGER` | 否 |  | 快照版本 |
| `turns` | `JSONB` | 否 |  | 转译内容快照数组 |
| `status` | `TEXT` | 否 |  | `queued` / `sending` / `sent` / `failed` / `retrying` / `cancelled` |
| `attempts` | `INTEGER` | 否 |  | 尝试次数 |
| `last_error_code` | `TEXT` | 是 |  | 最近错误码 |
| `created_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 创建时间 |
| `updated_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 更新时间 |

索引与约束：

| 名称 | 类型 | 定义 |
| --- | --- | --- |
| `outbound_messages_pkey` | PK | `PRIMARY KEY (id)` |
| `outbound_messages_account_id_fkey` | FK | `account_id REFERENCES lingow_accounts(id)` |
| `outbound_messages_account_idempotency_key` | UNIQUE | `(account_id, idempotency_key)` |
| `outbound_messages_account_created_order_idx` | Index | `(account_id, created_at DESC, id DESC)` |
| `outbound_messages_reject_snapshot_updates` | Trigger | 禁止更新消息身份与内容快照字段 |

### 4.18 `delivery_attempts`

消息投递尝试表。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | `TEXT` | 否 |  | 尝试 ID |
| `message_id` | `TEXT` | 否 |  | 消息 ID |
| `attempt_number` | `INTEGER` | 否 |  | 消息内第几次尝试，从 1 开始 |
| `status` | `TEXT` | 否 |  | `queued` / `sending` / `succeeded` / `failed` |
| `error_code` | `TEXT` | 是 |  | 错误码 |
| `next_attempt_at` | `TIMESTAMPTZ` | 是 |  | 下次尝试时间 |
| `started_at` | `TIMESTAMPTZ` | 是 |  | 开始时间 |
| `finished_at` | `TIMESTAMPTZ` | 是 |  | 完成时间 |
| `created_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 创建时间 |

### 4.19 `delivery_outbox`

投递 outbox 表。每个 `attempt_id` 只能对应一条 outbox 记录。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `id` | `TEXT` | 否 |  | Outbox ID |
| `attempt_id` | `TEXT` | 否 |  | 投递尝试 ID |
| `idempotency_key` | `TEXT` | 否 |  | Broker 投递幂等键 |
| `topic` | `TEXT` | 否 |  | 主题 |
| `event_key` | `TEXT` | 否 |  | 事件 key |
| `payload` | `JSONB` | 否 |  | 事件 payload |
| `available_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 可发布时间 |
| `published_at` | `TIMESTAMPTZ` | 是 |  | 发布时间 |
| `attempts` | `INTEGER` | 否 | `0` | 发布尝试次数 |
| `last_error` | `TEXT` | 是 |  | 最近错误 |
| `created_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 创建时间 |

### 4.20 `delivery_retry_requests`

人工重试幂等请求表，避免消息创建幂等键与重试幂等键混用。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `account_id` | `TEXT` | 否 |  | 请求账户 |
| `idempotency_key` | `TEXT` | 否 |  | 账户内重试幂等键 |
| `message_id` | `TEXT` | 否 |  | 消息 ID |
| `attempt_id` | `TEXT` | 否 |  | 新建的尝试 ID |
| `created_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 创建时间 |

## 5. Redis/Valkey 数据设计

仓库使用 Redis 7 兼容的 Redis Streams 作为队列，不使用 Redis 作为权威业务库。realtime usage outbox 还会保存永久的事件幂等状态。

### 5.1 用量事件流

来源：API 使用 `services/api/internal/usage/stream.go` 消费，realtime producer 使用 `services/realtime-audio/outbox/runtime.go` 发布。

| Key | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| Stream | Redis Stream | `lingow:usage:recorded` | realtime 使用 `USAGE_STREAM`，API 使用 `LINGOW_USAGE_STREAM`；两者为空时使用此默认值，部署时必须保持一致 |
| Group | Consumer Group | `lingow-usage` | API 使用 `LINGOW_USAGE_GROUP`，为空时使用此默认值 |
| Consumer | Consumer | 运行时派生 | API 优先使用 `LINGOW_USAGE_CONSUMER`；为空时使用 `<LINGOW_DELIVERY_CONSUMER>-usage`。仅直接调用 `NewValkeyUsageStream` 且传入空 consumer 时才使用 `usage-worker` |

Stream entry 字段：

| 字段 | 说明 |
| --- | --- |
| `payload` | 序列化后的 `usage.recorded` 事件 payload |

消费语义：

- `XGROUP CREATE ... MKSTREAM` 初始化消费组。
- `XREADGROUP` 读取新消息。
- `XAUTOCLAIM` 回收超过 `30s` 未 ACK 的 pending 消息。
- `Ack` 使用 `XACK`。
- `Nack` 不删除消息，保留在 pending list 中等待 auto-claim 重试。

realtime Valkey writer 的 dedup key 格式为 `<stream>:dedup:<topic>\0<idempotency-key>`，值为 payload hash 的十六进制值。该 key 的 TTL 当前为 `0`，即成功发布后永久保留；如果追加到 stream 失败则删除该 key。当前没有自动清理策略，因此其容量会随已发布 UsageFact 数量增长。

### 5.2 消息投递队列

来源：`services/api/internal/delivery/valkey_queue.go`

| Key | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| Stream | Redis Stream | `lingow:delivery` | 投递尝试队列 |
| Group | Consumer Group | `lingow-delivery` | 投递消费组 |
| Consumer | Consumer | `api` | 默认消费者名 |
| Delay Stream | Redis Stream | `lingow:delivery:delayed` | 延迟重试消息暂存 |
| Delay Key | Sorted Set | `lingow:delivery:delay` | 延迟消息 ID 到期时间索引，score 为 Unix 毫秒 |

主队列 entry 字段：

| 字段 | 说明 |
| --- | --- |
| `attempt_id` | `delivery_attempts.id`，投递尝试的持久身份 |
| `idempotency_key` | 投递幂等键 |

消费语义：

- `Enqueue` 使用 `XADD` 追加消息，不在 Redis 层去重。
- `delivery_attempts` 和仓库里的 `ClaimAttempt` 是投递副作用的幂等权威。
- `Receive` 先提升到期延迟消息，再回收 stale pending，再读取新消息。
- `Nack` 用 Lua 脚本原子执行：读取原 entry、写入 delay stream、写入 delay ZSET、`XACK` 原 entry、`XDEL` 原 entry。
- `promoteDue` 用 Lua 脚本按 ZSET 到期时间把延迟消息重新写回主 stream。

## 6. 历史兼容表

### 6.1 `voice_session_start_requests`

该表是兼容历史数据库的旧启动请求表。仓库迁移会给它写入 deprecation comment，新持久化路径必须使用 `voice_session_start_operations`。它不属于当前核心会话启动设计。

| 字段 | 类型 | 空 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `session_id` | `TEXT` | 否 |  | 会话 ID |
| `account_id` | `TEXT` | 否 |  | 账户 ID |
| `idempotency_key` | `TEXT` | 否 |  | 幂等键 |
| `request_hash` | `TEXT` | 否 |  | 请求体指纹 |
| `started_at` | `TIMESTAMPTZ` | 否 |  | 启动时间 |
| `created_at` | `TIMESTAMPTZ` | 否 | `CURRENT_TIMESTAMP` | 创建时间 |

## 7. DDL

以下 DDL 按仓库当前迁移整理核心业务对象，省略 `recordstore_schema_migrations` 与 `schema_migrations` 的迁移执行器写入逻辑、历史数据 `UPDATE` 和兼容性 `DO` 块，但保留业务表、索引、约束、函数和触发器。

```sql
CREATE TABLE lingow_accounts (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    phone_hash TEXT,
    merged_into TEXT REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    phone_hash_v2 TEXT,
    CONSTRAINT accounts_id_not_empty CHECK (id <> ''),
    CONSTRAINT accounts_kind_valid CHECK (kind IN ('anonymous', 'registered')),
    CONSTRAINT accounts_identity_valid CHECK (
        (kind = 'anonymous' AND phone_hash IS NULL AND phone_hash_v2 IS NULL)
        OR (kind = 'registered' AND (phone_hash IS NOT NULL OR phone_hash_v2 IS NOT NULL) AND merged_into IS NULL)
    )
);

CREATE UNIQUE INDEX lingow_accounts_phone_hash_key
    ON lingow_accounts (phone_hash)
    WHERE phone_hash IS NOT NULL;

CREATE UNIQUE INDEX lingow_accounts_phone_hash_v2_key
    ON lingow_accounts (phone_hash_v2)
    WHERE phone_hash_v2 IS NOT NULL;

CREATE OR REPLACE FUNCTION lingow_account_lineage(root_account_id TEXT)
RETURNS TABLE(account_id TEXT)
LANGUAGE sql
STABLE
AS $$
    WITH RECURSIVE lineage AS (
        SELECT id, ARRAY[id] AS visited
        FROM lingow_accounts
        WHERE id = root_account_id
        UNION ALL
        SELECT child.id, parent.visited || child.id
        FROM lingow_accounts AS child
        JOIN lineage AS parent ON child.merged_into = parent.id
        WHERE NOT child.id = ANY(parent.visited)
    )
    SELECT id FROM lineage;
$$;

CREATE TABLE lingow_phone_challenges (
    id TEXT PRIMARY KEY,
    phone_hash TEXT NOT NULL,
    code_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    attempts SMALLINT NOT NULL DEFAULT 0,
    max_attempts SMALLINT NOT NULL DEFAULT 5,
    last_attempt_at TIMESTAMPTZ,
    legacy_phone_hash TEXT NOT NULL,
    digest_version SMALLINT NOT NULL DEFAULT 1,
    CONSTRAINT lingow_phone_challenges_id_not_empty CHECK (id <> ''),
    CONSTRAINT lingow_phone_challenges_phone_hash_not_empty CHECK (phone_hash <> ''),
    CONSTRAINT lingow_phone_challenges_code_hash_not_empty CHECK (code_hash <> ''),
    CONSTRAINT lingow_phone_challenges_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT lingow_phone_challenges_used_at_valid CHECK (used_at IS NULL OR used_at >= created_at),
    CONSTRAINT lingow_phone_challenges_attempts_valid CHECK (attempts >= 0 AND attempts <= max_attempts),
    CONSTRAINT lingow_phone_challenges_max_attempts_valid CHECK (max_attempts BETWEEN 1 AND 10),
    CONSTRAINT lingow_phone_challenges_last_attempt_valid CHECK (last_attempt_at IS NULL OR last_attempt_at >= created_at),
    CONSTRAINT lingow_phone_challenges_digest_version_valid CHECK (digest_version IN (1, 2))
);

CREATE INDEX lingow_phone_challenges_phone_expiry_idx
    ON lingow_phone_challenges (phone_hash, expires_at DESC);
CREATE INDEX lingow_phone_challenges_phone_created_idx
    ON lingow_phone_challenges (phone_hash, created_at DESC);
CREATE INDEX lingow_phone_challenges_legacy_phone_created_idx
    ON lingow_phone_challenges (legacy_phone_hash, created_at DESC);

CREATE TABLE email_bind_challenges (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    destination_ref TEXT NOT NULL,
    email TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT email_bind_challenges_id_not_empty CHECK (id <> ''),
    CONSTRAINT email_bind_challenges_account_not_empty CHECK (account_id <> ''),
    CONSTRAINT email_bind_challenges_ref_not_empty CHECK (destination_ref <> ''),
    CONSTRAINT email_bind_challenges_email_not_empty CHECK (email <> ''),
    CONSTRAINT email_bind_challenges_token_hash_not_empty CHECK (token_hash <> ''),
    CONSTRAINT email_bind_challenges_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT email_bind_challenges_used_at_valid CHECK (used_at IS NULL OR used_at >= created_at)
);

CREATE UNIQUE INDEX email_bind_challenges_token_hash_key
    ON email_bind_challenges (token_hash);
CREATE INDEX email_bind_challenges_account_active_idx
    ON email_bind_challenges (account_id, created_at DESC)
    WHERE used_at IS NULL;

CREATE TABLE lingow_auth_sessions (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    refresh_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT lingow_auth_sessions_id_not_empty CHECK (id <> ''),
    CONSTRAINT lingow_auth_sessions_refresh_hash_not_empty CHECK (refresh_hash <> ''),
    CONSTRAINT lingow_auth_sessions_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT lingow_auth_sessions_revoked_at_valid CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE UNIQUE INDEX lingow_auth_sessions_refresh_hash_key
    ON lingow_auth_sessions (refresh_hash);
CREATE INDEX lingow_auth_sessions_active_refresh_lookup_idx
    ON lingow_auth_sessions (refresh_hash, expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE voice_sessions (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    status TEXT NOT NULL,
    audio_config JSONB NOT NULL,
    capabilities JSONB NOT NULL,
    failure_error_code TEXT,
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT voice_sessions_id_not_empty CHECK (id <> ''),
    CONSTRAINT voice_sessions_status_valid CHECK (status IN ('created', 'active', 'ended', 'failed')),
    CONSTRAINT voice_sessions_config_objects CHECK (
        jsonb_typeof(audio_config) = 'object'
        AND jsonb_typeof(capabilities) = 'object'
    ),
    CONSTRAINT voice_sessions_timestamps_valid CHECK (
        (status = 'created' AND started_at IS NULL AND ended_at IS NULL AND failure_error_code IS NULL)
        OR (status = 'active' AND started_at IS NOT NULL AND ended_at IS NULL AND failure_error_code IS NULL)
        OR (
            status = 'ended'
            AND ended_at IS NOT NULL
            AND failure_error_code IS NULL
            AND (
                (started_at IS NULL AND ended_at >= created_at)
                OR (started_at IS NOT NULL AND ended_at >= started_at)
            )
        )
        OR (
            status = 'failed'
            AND started_at IS NOT NULL
            AND ended_at IS NOT NULL
            AND ended_at >= started_at
            AND failure_error_code IS NOT NULL
        )
    ),
    CONSTRAINT voice_sessions_failure_error_not_empty CHECK (failure_error_code IS NULL OR failure_error_code <> ''),
    CONSTRAINT voice_sessions_id_account_id_key UNIQUE (id, account_id)
);

CREATE INDEX voice_sessions_account_created_order_idx
    ON voice_sessions (account_id, created_at DESC, id DESC);
CREATE INDEX voice_sessions_account_status_created_order_idx
    ON voice_sessions (account_id, status, created_at DESC, id DESC);

CREATE TABLE voice_session_create_requests (
    account_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    session_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (account_id, idempotency_key),
    CONSTRAINT voice_session_create_requests_key_not_empty CHECK (idempotency_key <> ''),
    CONSTRAINT voice_session_create_requests_hash_not_empty CHECK (request_hash <> ''),
    CONSTRAINT voice_session_create_requests_session_key
        FOREIGN KEY (session_id, account_id)
        REFERENCES voice_sessions (id, account_id)
        ON DELETE RESTRICT
);

CREATE TABLE voice_session_start_operations (
    operation_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    status TEXT NOT NULL,
    compensation_claim_id TEXT,
    started_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT voice_session_start_operations_id_not_empty CHECK (operation_id <> ''),
    CONSTRAINT voice_session_start_operations_key_not_empty CHECK (idempotency_key <> ''),
    CONSTRAINT voice_session_start_operations_hash_not_empty CHECK (request_hash <> ''),
    CONSTRAINT voice_session_start_operations_status_valid CHECK (
        status IN ('pending', 'compensating', 'completed', 'compensated', 'compensation_failed')
    ),
    CONSTRAINT voice_session_start_operations_claim_id_not_empty CHECK (compensation_claim_id IS NULL OR compensation_claim_id <> ''),
    CONSTRAINT voice_session_start_operations_updated_at_valid CHECK (updated_at >= created_at),
    CONSTRAINT voice_session_start_operations_state_valid CHECK (
        (status = 'pending' AND started_at IS NULL AND compensation_claim_id IS NULL)
        OR (status = 'compensating' AND started_at IS NULL AND compensation_claim_id IS NOT NULL)
        OR (status = 'completed' AND started_at IS NOT NULL AND compensation_claim_id IS NULL)
        OR (status IN ('compensated', 'compensation_failed') AND started_at IS NULL AND compensation_claim_id IS NOT NULL)
    ),
    CONSTRAINT voice_session_start_operations_key_unique UNIQUE (account_id, idempotency_key),
    CONSTRAINT voice_session_start_operations_session_key
        FOREIGN KEY (session_id, account_id)
        REFERENCES voice_sessions (id, account_id)
        ON DELETE RESTRICT
);

CREATE UNIQUE INDEX voice_session_start_operations_one_unfinished_per_session
    ON voice_session_start_operations (session_id)
    WHERE status IN ('pending', 'compensating', 'compensation_failed');
CREATE INDEX voice_session_start_operations_account_session_key_idx
    ON voice_session_start_operations (account_id, session_id, idempotency_key);

CREATE TABLE voice_session_end_intents (
    session_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    requested_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    trace_id TEXT NOT NULL,
    retry_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    recovery_owner TEXT,
    recovery_lease_expires_at TIMESTAMPTZ,
    PRIMARY KEY (session_id, account_id),
    CONSTRAINT voice_session_end_intents_reason_valid CHECK (reason IN ('user_requested', 'operator_cancelled', 'client_disconnected')),
    CONSTRAINT voice_session_end_intents_key_not_empty CHECK (idempotency_key <> ''),
    CONSTRAINT voice_session_end_intents_hash_not_empty CHECK (request_hash <> ''),
    CONSTRAINT voice_session_end_intents_completion_valid CHECK (completed_at IS NULL OR completed_at >= requested_at),
    CONSTRAINT voice_session_end_intents_trace_not_empty CHECK (trace_id <> ''),
    CONSTRAINT voice_session_end_intents_retry_count_valid CHECK (retry_count >= 0),
    CONSTRAINT voice_session_end_intents_recovery_lease_valid CHECK (
        (recovery_owner IS NULL AND recovery_lease_expires_at IS NULL)
        OR (recovery_owner IS NOT NULL AND recovery_owner <> '' AND recovery_lease_expires_at IS NOT NULL)
    ),
    CONSTRAINT voice_session_end_intents_key_unique UNIQUE (account_id, idempotency_key),
    CONSTRAINT voice_session_end_intents_session_key
        FOREIGN KEY (session_id, account_id)
        REFERENCES voice_sessions (id, account_id)
        ON DELETE RESTRICT
);

CREATE INDEX voice_session_end_intents_recovery_due_idx
    ON voice_session_end_intents (next_attempt_at, requested_at, session_id)
    WHERE completed_at IS NULL;

CREATE TABLE supported_languages (
    language_code VARCHAR(10) PRIMARY KEY,
    display_name VARCHAR(64) NOT NULL,
    display_name_en VARCHAR(64) NOT NULL DEFAULT '',
    supports_as_source BOOLEAN NOT NULL DEFAULT TRUE,
    supports_as_target BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INT NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE voice_session_language_configs (
    id VARCHAR(26) PRIMARY KEY,
    session_id TEXT NOT NULL,
    version INT NOT NULL,
    language_pairs JSONB NOT NULL,
    status VARCHAR(20) NOT NULL,
    effective_from TIMESTAMPTZ NOT NULL,
    effective_until TIMESTAMPTZ,
    created_by VARCHAR(64) NOT NULL,
    idempotency_key VARCHAR(128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    request_fingerprint VARCHAR(128),
    CONSTRAINT chk_lang_config_status CHECK (status IN ('active', 'superseded', 'expired'))
);

CREATE UNIQUE INDEX idx_lang_config_active
    ON voice_session_language_configs (session_id, status)
    WHERE status = 'active';
CREATE UNIQUE INDEX idx_lang_config_version
    ON voice_session_language_configs (session_id, version);
CREATE UNIQUE INDEX idx_lang_config_idempotency
    ON voice_session_language_configs (idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX idx_lang_config_session_version_desc
    ON voice_session_language_configs (session_id, version DESC);

CREATE TABLE voice_session_participants (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    speaker_code TEXT NOT NULL,
    display_name TEXT,
    provider_speaker_id TEXT,
    voice_profile_id TEXT,
    confidence DOUBLE PRECISION,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT voice_session_participants_id_not_empty CHECK (id <> ''),
    CONSTRAINT voice_session_participants_session_id_not_empty CHECK (session_id <> ''),
    CONSTRAINT voice_session_participants_speaker_code_not_empty CHECK (speaker_code <> ''),
    CONSTRAINT voice_session_participants_session_speaker_code_key UNIQUE (session_id, speaker_code),
    CONSTRAINT voice_session_participants_session_id_id_key UNIQUE (session_id, id)
);

CREATE UNIQUE INDEX voice_session_participants_session_provider_speaker_id_key
    ON voice_session_participants (session_id, provider_speaker_id)
    WHERE provider_speaker_id IS NOT NULL;
CREATE INDEX voice_session_participants_session_speaker_order_idx
    ON voice_session_participants (session_id, speaker_code ASC, id ASC);

CREATE TABLE voice_turns (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    event_payload_hash BYTEA NOT NULL,
    session_id TEXT NOT NULL,
    participant_id TEXT,
    speaker_code TEXT NOT NULL,
    display_name TEXT,
    provider_speaker_id TEXT,
    voice_profile_id TEXT,
    sequence_no BIGINT NOT NULL,
    source_language TEXT NOT NULL,
    target_language TEXT NOT NULL,
    language_config_version BIGINT NOT NULL,
    source_text TEXT NOT NULL,
    translated_text TEXT NOT NULL,
    speaker_confidence DOUBLE PRECISION,
    attribution_status TEXT NOT NULL,
    corrected_by TEXT,
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ NOT NULL,
    corrected_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT voice_turns_id_not_empty CHECK (id <> ''),
    CONSTRAINT voice_turns_event_id_not_empty CHECK (event_id <> ''),
    CONSTRAINT voice_turns_event_payload_hash_length CHECK (octet_length(event_payload_hash) = 32),
    CONSTRAINT voice_turns_session_id_not_empty CHECK (session_id <> ''),
    CONSTRAINT voice_turns_speaker_code_not_empty CHECK (speaker_code <> ''),
    CONSTRAINT voice_turns_sequence_no_positive CHECK (sequence_no >= 1),
    CONSTRAINT voice_turns_source_language_not_empty CHECK (source_language <> ''),
    CONSTRAINT voice_turns_target_language_not_empty CHECK (target_language <> ''),
    CONSTRAINT voice_turns_language_config_version_positive CHECK (language_config_version >= 1),
    CONSTRAINT voice_turns_source_text_not_empty CHECK (source_text <> ''),
    CONSTRAINT voice_turns_translated_text_not_empty CHECK (translated_text <> ''),
    CONSTRAINT voice_turns_attribution_status_valid CHECK (attribution_status IN ('pending', 'provisional', 'confirmed', 'corrected')),
    CONSTRAINT voice_turns_corrected_by_valid CHECK (corrected_by IS NULL OR corrected_by = 'system'),
    CONSTRAINT voice_turns_time_order_valid CHECK (ended_at >= started_at),
    CONSTRAINT voice_turns_session_sequence_no_key UNIQUE (session_id, sequence_no),
    CONSTRAINT voice_turns_session_participant_foreign_key
        FOREIGN KEY (session_id, participant_id)
        REFERENCES voice_session_participants (session_id, id)
        ON UPDATE RESTRICT
        ON DELETE RESTRICT
);

CREATE INDEX voice_turns_session_sequence_order_idx
    ON voice_turns (session_id, sequence_no ASC, id ASC);
CREATE INDEX voice_turns_history_created_order_idx
    ON voice_turns (created_at DESC, id DESC);
CREATE INDEX voice_turns_session_history_order_idx
    ON voice_turns (session_id, created_at DESC, id DESC);

CREATE TABLE attribution_tasks (
    task_id TEXT PRIMARY KEY,
    turn_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    task_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    receipt TEXT,
    locked_until TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT attribution_tasks_task_id_not_empty CHECK (task_id <> ''),
    CONSTRAINT attribution_tasks_turn_id_not_empty CHECK (turn_id <> ''),
    CONSTRAINT attribution_tasks_session_id_not_empty CHECK (session_id <> ''),
    CONSTRAINT attribution_tasks_account_id_not_empty CHECK (account_id <> ''),
    CONSTRAINT attribution_tasks_task_type_valid CHECK (task_type IN ('participant_mapping', 'turn_attribution')),
    CONSTRAINT attribution_tasks_status_valid CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    CONSTRAINT attribution_tasks_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT attribution_tasks_available_at_valid CHECK (available_at >= created_at),
    CONSTRAINT attribution_tasks_receipt_state_valid CHECK (
        (status = 'processing' AND receipt IS NOT NULL AND receipt <> '' AND locked_until IS NOT NULL)
        OR (status IN ('pending', 'completed', 'failed') AND receipt IS NULL AND locked_until IS NULL)
    ),
    CONSTRAINT attribution_tasks_turn_id_key UNIQUE (turn_id)
);

CREATE INDEX attribution_tasks_available_idx
    ON attribution_tasks (available_at ASC, created_at ASC, task_id ASC)
    WHERE status = 'pending';
CREATE INDEX attribution_tasks_lease_idx
    ON attribution_tasks (locked_until ASC, created_at ASC, task_id ASC)
    WHERE status = 'processing';

CREATE TABLE final_turn_outbox (
    event_id TEXT PRIMARY KEY,
    turn_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    sequence_no BIGINT NOT NULL,
    payload_hash BYTEA NOT NULL,
    payload JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    receipt TEXT,
    locked_until TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    rejected_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT final_turn_outbox_event_id_not_empty CHECK (event_id <> ''),
    CONSTRAINT final_turn_outbox_turn_id_not_empty CHECK (turn_id <> ''),
    CONSTRAINT final_turn_outbox_session_id_not_empty CHECK (session_id <> ''),
    CONSTRAINT final_turn_outbox_sequence_positive CHECK (sequence_no > 0),
    CONSTRAINT final_turn_outbox_payload_hash_length CHECK (octet_length(payload_hash) = 32),
    CONSTRAINT final_turn_outbox_payload_object CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT final_turn_outbox_status_valid CHECK (status IN ('pending', 'processing', 'acked', 'rejected')),
    CONSTRAINT final_turn_outbox_available_at_valid CHECK (available_at >= created_at),
    CONSTRAINT final_turn_outbox_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT final_turn_outbox_receipt_state_valid CHECK (
        (status = 'processing' AND receipt IS NOT NULL AND receipt <> '' AND locked_until IS NOT NULL)
        OR (status IN ('pending', 'acked', 'rejected') AND receipt IS NULL AND locked_until IS NULL)
    )
);

CREATE INDEX final_turn_outbox_available_idx
    ON final_turn_outbox (available_at ASC, created_at ASC, event_id ASC)
    WHERE status = 'pending';
CREATE INDEX final_turn_outbox_lease_idx
    ON final_turn_outbox (locked_until ASC, created_at ASC, event_id ASC)
    WHERE status = 'processing';

CREATE TABLE lingow_usage_records (
    event_version INTEGER NOT NULL,
    event_id TEXT PRIMARY KEY,
    trace_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    payload_hash BYTEA NOT NULL,
    account_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    service_type TEXT NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    input_tokens BIGINT NOT NULL,
    output_tokens BIGINT NOT NULL,
    audio_duration_ms BIGINT NOT NULL,
    cost_amount NUMERIC(20, 8),
    currency TEXT,
    occurred_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT lingow_usage_records_event_id_not_empty CHECK (event_id <> ''),
    CONSTRAINT lingow_usage_records_event_version_valid CHECK (event_version = 1),
    CONSTRAINT lingow_usage_records_trace_id_not_empty CHECK (trace_id <> ''),
    CONSTRAINT lingow_usage_records_idempotency_key_not_empty CHECK (idempotency_key <> ''),
    CONSTRAINT lingow_usage_records_payload_hash_length CHECK (octet_length(payload_hash) = 32),
    CONSTRAINT lingow_usage_records_turn_id_not_empty CHECK (turn_id <> ''),
    CONSTRAINT lingow_usage_records_service_type_valid CHECK (service_type IN ('asr', 'translation', 'tts', 'diarization')),
    CONSTRAINT lingow_usage_records_provider_not_empty CHECK (provider <> ''),
    CONSTRAINT lingow_usage_records_model_not_empty CHECK (model <> ''),
    CONSTRAINT lingow_usage_records_measurements_nonnegative CHECK (
        input_tokens >= 0 AND output_tokens >= 0 AND audio_duration_ms >= 0
        AND (cost_amount IS NULL OR cost_amount >= 0)
    ),
    CONSTRAINT lingow_usage_records_currency_valid CHECK (currency IS NULL OR currency ~ '^[A-Z]{3}$'),
    CONSTRAINT lingow_usage_records_pricing_pair_valid CHECK ((cost_amount IS NULL) = (currency IS NULL)),
    CONSTRAINT lingow_usage_records_session_key
        FOREIGN KEY (session_id, account_id)
        REFERENCES voice_sessions (id, account_id)
        ON DELETE RESTRICT
);

CREATE UNIQUE INDEX lingow_usage_records_idempotency_key
    ON lingow_usage_records (idempotency_key);
CREATE INDEX lingow_usage_records_session_service_occurred_idx
    ON lingow_usage_records (session_id, service_type, occurred_at ASC, event_id ASC);
CREATE INDEX lingow_usage_records_account_occurred_idx
    ON lingow_usage_records (account_id, occurred_at ASC, event_id ASC);

CREATE TABLE account_destinations (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    channel TEXT NOT NULL,
    destination_ref TEXT NOT NULL,
    provider_target_ciphertext BYTEA NOT NULL,
    key_version TEXT NOT NULL,
    verified_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT account_destinations_id_not_empty CHECK (id <> ''),
    CONSTRAINT account_destinations_channel_valid CHECK (channel IN ('email', 'wechat')),
    CONSTRAINT account_destinations_ref_not_empty CHECK (destination_ref <> ''),
    CONSTRAINT account_destinations_target_not_empty CHECK (octet_length(provider_target_ciphertext) > 0),
    CONSTRAINT account_destinations_key_version_not_empty CHECK (key_version <> ''),
    CONSTRAINT account_destinations_verification_valid CHECK (revoked_at IS NULL OR verified_at IS NOT NULL),
    CONSTRAINT account_destinations_revocation_valid CHECK (revoked_at IS NULL OR revoked_at >= verified_at),
    CONSTRAINT account_destinations_updated_at_valid CHECK (updated_at >= created_at),
    CONSTRAINT account_destinations_account_channel_ref_key UNIQUE (account_id, channel, destination_ref)
);

CREATE INDEX account_destinations_verified_lookup_idx
    ON account_destinations (account_id, channel, destination_ref)
    WHERE verified_at IS NOT NULL AND revoked_at IS NULL;

CREATE TABLE message_preferences (
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    channel TEXT NOT NULL,
    enabled BOOLEAN NOT NULL,
    verified BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (account_id, channel),
    CONSTRAINT message_preferences_channel_valid CHECK (channel IN ('email', 'wechat'))
);

CREATE TABLE outbound_messages (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL,
    channel TEXT NOT NULL,
    destination_ref TEXT NOT NULL,
    snapshot_version INTEGER NOT NULL,
    turns JSONB NOT NULL,
    status TEXT NOT NULL,
    attempts INTEGER NOT NULL,
    last_error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT outbound_messages_id_not_empty CHECK (id <> ''),
    CONSTRAINT outbound_messages_idempotency_key_not_empty CHECK (idempotency_key <> ''),
    CONSTRAINT outbound_messages_channel_valid CHECK (channel IN ('email', 'wechat')),
    CONSTRAINT outbound_messages_destination_ref_not_empty CHECK (destination_ref <> ''),
    CONSTRAINT outbound_messages_snapshot_version_positive CHECK (snapshot_version >= 1),
    CONSTRAINT outbound_messages_turns_array CHECK (jsonb_typeof(turns) = 'array' AND jsonb_array_length(turns) > 0),
    CONSTRAINT outbound_messages_status_valid CHECK (status IN ('queued', 'sending', 'sent', 'failed', 'retrying', 'cancelled')),
    CONSTRAINT outbound_messages_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT outbound_messages_last_error_not_empty CHECK (last_error_code IS NULL OR last_error_code <> ''),
    CONSTRAINT outbound_messages_updated_at_valid CHECK (updated_at >= created_at),
    CONSTRAINT outbound_messages_account_idempotency_key UNIQUE (account_id, idempotency_key)
);

CREATE INDEX outbound_messages_account_created_order_idx
    ON outbound_messages (account_id, created_at DESC, id DESC);

CREATE TABLE delivery_attempts (
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL REFERENCES outbound_messages (id) ON DELETE RESTRICT,
    attempt_number INTEGER NOT NULL,
    status TEXT NOT NULL,
    error_code TEXT,
    next_attempt_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT delivery_attempts_id_not_empty CHECK (id <> ''),
    CONSTRAINT delivery_attempts_number_positive CHECK (attempt_number >= 1),
    CONSTRAINT delivery_attempts_status_valid CHECK (status IN ('queued', 'sending', 'succeeded', 'failed')),
    CONSTRAINT delivery_attempts_error_not_empty CHECK (error_code IS NULL OR error_code <> ''),
    CONSTRAINT delivery_attempts_timestamps_valid CHECK (
        (started_at IS NULL OR started_at >= created_at)
        AND (finished_at IS NULL OR (started_at IS NOT NULL AND finished_at >= started_at))
        AND (next_attempt_at IS NULL OR next_attempt_at >= created_at)
    ),
    CONSTRAINT delivery_attempts_message_number_key UNIQUE (message_id, attempt_number)
);

CREATE INDEX delivery_attempts_message_order_idx
    ON delivery_attempts (message_id, attempt_number ASC);
CREATE INDEX delivery_attempts_queued_schedule_idx
    ON delivery_attempts (next_attempt_at ASC, created_at ASC, id ASC)
    WHERE status = 'queued';

CREATE TABLE delivery_outbox (
    id TEXT PRIMARY KEY,
    attempt_id TEXT NOT NULL REFERENCES delivery_attempts (id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL,
    topic TEXT NOT NULL,
    event_key TEXT NOT NULL,
    payload JSONB NOT NULL,
    available_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT delivery_outbox_id_not_empty CHECK (id <> ''),
    CONSTRAINT delivery_outbox_idempotency_key_not_empty CHECK (idempotency_key <> ''),
    CONSTRAINT delivery_outbox_topic_not_empty CHECK (topic <> ''),
    CONSTRAINT delivery_outbox_event_key_not_empty CHECK (event_key <> ''),
    CONSTRAINT delivery_outbox_payload_object CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT delivery_outbox_available_at_valid CHECK (available_at >= created_at),
    CONSTRAINT delivery_outbox_published_at_valid CHECK (published_at IS NULL OR published_at >= created_at),
    CONSTRAINT delivery_outbox_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT delivery_outbox_last_error_not_empty CHECK (last_error IS NULL OR last_error <> ''),
    CONSTRAINT delivery_outbox_attempt_key UNIQUE (attempt_id)
);

CREATE INDEX delivery_outbox_unpublished_schedule_idx
    ON delivery_outbox (available_at ASC, created_at ASC, id ASC)
    WHERE published_at IS NULL;

CREATE TABLE delivery_retry_requests (
    account_id TEXT NOT NULL REFERENCES lingow_accounts (id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL,
    message_id TEXT NOT NULL REFERENCES outbound_messages (id) ON DELETE RESTRICT,
    attempt_id TEXT NOT NULL REFERENCES delivery_attempts (id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT delivery_retry_requests_account_key PRIMARY KEY (account_id, idempotency_key),
    CONSTRAINT delivery_retry_requests_attempt_key UNIQUE (attempt_id),
    CONSTRAINT delivery_retry_requests_account_not_empty CHECK (account_id <> ''),
    CONSTRAINT delivery_retry_requests_key_not_empty CHECK (idempotency_key <> ''),
    CONSTRAINT delivery_retry_requests_message_not_empty CHECK (message_id <> ''),
    CONSTRAINT delivery_retry_requests_attempt_not_empty CHECK (attempt_id <> '')
);

CREATE INDEX delivery_retry_requests_message_created_idx
    ON delivery_retry_requests (message_id, created_at DESC);

CREATE FUNCTION recordstore_reject_voice_turn_immutable_updates()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.event_id IS DISTINCT FROM OLD.event_id
        OR NEW.event_payload_hash IS DISTINCT FROM OLD.event_payload_hash
        OR NEW.session_id IS DISTINCT FROM OLD.session_id
        OR NEW.sequence_no IS DISTINCT FROM OLD.sequence_no
        OR NEW.source_language IS DISTINCT FROM OLD.source_language
        OR NEW.target_language IS DISTINCT FROM OLD.target_language
        OR NEW.language_config_version IS DISTINCT FROM OLD.language_config_version
        OR NEW.source_text IS DISTINCT FROM OLD.source_text
        OR NEW.translated_text IS DISTINCT FROM OLD.translated_text
        OR NEW.started_at IS DISTINCT FROM OLD.started_at
        OR NEW.ended_at IS DISTINCT FROM OLD.ended_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'voice turn immutable fields cannot be updated';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER voice_turns_reject_immutable_updates
    BEFORE UPDATE ON voice_turns
    FOR EACH ROW
    EXECUTE FUNCTION recordstore_reject_voice_turn_immutable_updates();

CREATE FUNCTION recordstore_reject_usage_record_updates()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'usage records are immutable';
END;
$$;

CREATE TRIGGER lingow_usage_records_reject_updates
    BEFORE UPDATE ON lingow_usage_records
    FOR EACH ROW
    EXECUTE FUNCTION recordstore_reject_usage_record_updates();

CREATE FUNCTION recordstore_reject_outbound_message_snapshot_updates()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.account_id IS DISTINCT FROM OLD.account_id
        OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
        OR NEW.channel IS DISTINCT FROM OLD.channel
        OR NEW.destination_ref IS DISTINCT FROM OLD.destination_ref
        OR NEW.snapshot_version IS DISTINCT FROM OLD.snapshot_version
        OR NEW.turns IS DISTINCT FROM OLD.turns
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'outbound message snapshot fields cannot be updated';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER outbound_messages_reject_snapshot_updates
    BEFORE UPDATE ON outbound_messages
    FOR EACH ROW
    EXECUTE FUNCTION recordstore_reject_outbound_message_snapshot_updates();

CREATE FUNCTION recordstore_reject_final_turn_outbox_payload_updates()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.event_id IS DISTINCT FROM OLD.event_id
        OR NEW.turn_id IS DISTINCT FROM OLD.turn_id
        OR NEW.session_id IS DISTINCT FROM OLD.session_id
        OR NEW.sequence_no IS DISTINCT FROM OLD.sequence_no
        OR NEW.payload_hash IS DISTINCT FROM OLD.payload_hash
        OR NEW.payload IS DISTINCT FROM OLD.payload
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'final turn outbox payload fields cannot be updated';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER final_turn_outbox_reject_payload_updates
    BEFORE UPDATE ON final_turn_outbox
    FOR EACH ROW
    EXECUTE FUNCTION recordstore_reject_final_turn_outbox_payload_updates();
```

## 8. 与 issue #76 的对齐和差异

- 已覆盖 `voice_sessions`、`voice_session_participants`、`voice_turns`、`lingow_usage_records` 的核心链路。
- issue 中的 `voice_usage_records` 在仓库中命名为 `lingow_usage_records`，并按事件 ID 与幂等键做不可变事实存储。
- issue 中的 `voice_session_usage_summaries` 当前没有物化表；仓库通过 `services/api/internal/usage/postgres.go` 对 `lingow_usage_records` 按账户、会话、服务类型、币种实时聚合。
- issue 后续评论提出的简化语言表 `language_1_code`、`language_2_code` 尚未落到当前仓库；仓库仍以 `voice_session_language_configs.language_pairs` 和 `version` 作为实现。
- issue 中 `corrected_by` 提到 `system` / `user` / `admin`，当前仓库约束只允许 `system`，表示目前只支持系统侧归属修正。
- issue 中建议的账户与消息投递扩展在仓库中已落为 `lingow_accounts`、`lingow_auth_sessions`、`email_bind_challenges`、`account_destinations`、`message_preferences`、`outbound_messages`、`delivery_attempts`、`delivery_outbox`、`delivery_retry_requests`，当前渠道为 `email` / `wechat`。

## 9. 设计复杂度与简化候选

以下条目不是文档建议立即删除的内容，而是当前仓库相对 MVP 可能偏重的设计点，供后续产品和工程判断。

| 设计点 | 当前合理性 | 可能过度的条件 | 建议判断 |
| --- | --- | --- | --- |
| `voice_profile_id` | 为后续声纹档案或跨会话身份识别保留外部引用 | MVP 只做会话内临时说话人，且短期不建设声纹档案 | 保留时应标注为可空外部引用；若要强约束，先设计声纹档案、隐私删除和撤销策略 |
| `provider_speaker_id` | 用于复用供应商在会话内输出的说话人聚类 | 同一会话会接入多个 diarization provider 或 provider 聚类 ID 不稳定 | 若多 provider 成为需求，应增加 provider namespace 或改为只保留在事件 payload |
| Turn 侧 speaker 快照字段 | 保留实时阶段识别结果和最终修正归属之间的差异 | 产品只展示修正后的最终参与者，不需要历史快照 | 当前合理；若简化，可减少 `voice_turns` 上的 speaker 快照字段 |
| `voice_session_language_configs` 版本化设计 | 支持配置历史、幂等重放和 `expected_version` 并发控制 | 只需要“当前两种语言”且不需要配置历史 | 可改为单行当前配置；当前仓库代码已采用版本化设计，文档按现状记录 |
| `voice_session_start_operations` 与 `voice_session_end_intents` 恢复字段 | 解决 API 与 realtime-audio 跨服务 Start/Stop 副作用一致性 | 如果 Start/Stop 永远是单进程同步调用 | 当前不建议删，属于可靠性设计 |
| `final_turn_outbox` | 承载 realtime-audio 到 API 的入站 FinalTurn 消费状态 | 如果 FinalTurn 改为 API 同步写入且无重试 | 当前合理，不应与出站 `delivery_outbox` 合并 |
| 消息投递表组 | issue 评论和仓库代码已覆盖外部消息投递 | 如果只保留语音翻译核心，不做外部渠道发送 | 应作为控制面扩展模块呈现，不混同为语音会话必需路径 |

## 10. 后续建议

- 如果要让 issue #76 的“简化语言配置表”成为真源，需要新增迁移替换或演进 `voice_session_language_configs`，并同步 contracts 与代码。
- 如果用量汇总查询压力升高，再增加物化汇总表；当前仓库代码不需要 `voice_session_usage_summaries`。
- 如果后续允许用户或管理员手动修正说话人归属，需要放宽 `voice_turns_corrected_by_valid` 并补充审计字段或修正历史表。
- 如果 `voice_profile_id` 要从外部引用变成强约束，需要先设计声纹档案表及隐私、撤销、合规策略。
