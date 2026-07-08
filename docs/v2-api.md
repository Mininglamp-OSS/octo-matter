# Octo-Matter v2 API (matter-v2 branch)

Matter v2 turns the lightweight task service into the human×agent delegation
workspace from the Matter_New_v2 design docs: six-state machine, sub-matters,
collaboration modes, transactional-outbox doorbells, watchdog, feedback
(圈一笔), projects, scheduled matters, smart-summary drafts, and the
internal bot-task queue the fleet PoC expects.

All v1 endpoints keep working (octo-web's dmworktodo panel stays functional).
Everything below is additive.

## Auth (unchanged)

- User: `token: <im token>` header → octo-server `POST /v1/auth/verify`
- Bot:  `Authorization: Bearer <bot token>` → `POST /v1/auth/verify-bot`
- All `/api/v1/*` calls additionally require `X-Space-Id`.
- Internal endpoints use `X-Internal-Token` (constant-time compare, fail closed).

When served behind nginx the service root is `/matter/`. The embedded UI
calculates its API base from the current path, so `/matter/`, `/matter/ui`,
and `/matter/ui/` all call `/matter/api/v1`; direct service access still calls
`/api/v1`.

## Status machine

`open(待办) → in_progress(进行中) → review(审核中) → done(完成)`
plus `blocked(受阻)`, `cancelled(取消, terminal)`, legacy `archived`.

Transition guard (server-enforced, producer matrix from doc 02.5):

| edge | allowed |
|------|---------|
| open→in_progress | assignee / leader / creator |
| in_progress→review | assignee / leader / creator |
| in_progress↔blocked | assignee/leader (reason kind=agent) · system watchdog (kind=system) · creator |
| review→in_progress | creator/leader, or system-derived from feedback |
| →done | top matter: creator only; child: parent leader or parent creator. A bot that is the matter's own assignee/leader can never complete it (no self-grading) |
| →cancelled | creator or parent leader |
| done→in_progress / done→open | creator (undo) |
| archived | creator only (legacy) |

Parent→done additionally requires every non-cancelled child to be done.

Concurrency / fencing on `PUT /matters/:id/status`:

```json
{ "status": "review",
  "expected_version": 4,        // optional CAS; mismatch → 409 VERSION_CONFLICT
  "assignment_epoch": 2,        // required for bot writes; stale → 409 EPOCH_STALE
  "reason": "缺少 X 数据",       // required when status=blocked
  "summary": "one-line outcome" // optional; recorded in activity detail
}
```

Same-status submission is a no-op (idempotent retry). Every transition bumps
`version`, stamps `last_transition_at` / `last_activity_at`, appends a
`status_changed` activity `{from,to,producer,reason?}`, bumps the parent's
`events_seq` when the matter has a parent, and enqueues doorbells (below)
in the same DB transaction.

## Matter object (new fields, all optional/nullable)

```
leader_uid            负责人/Team Leader (single uid; assignees 仍是多人列表)
parent_matter_id      sub-matter 递归
mode                  solo|split|swarm|roundtable|pipeline|critic (on parent)
step_id, step_order   派活幂等键/排序 (child)
project_id            归属项目
assignment_epoch      改派次数; leader_uid 变更时 +1
version               乐观锁
events_seq, processed_seq, inflight   合并必达 (parent)
expected_duration_minutes, last_activity_at, last_transition_at
block_reason_kind ('agent'|'system'), block_reason_text
schedule_id, scheduled_at             定时建单幂等键
```

`POST /api/v1/matters` accepts: `title, description, assignee_ids, leader_uid,
parent_matter_id, step_id, step_order, mode, project_id,
expected_duration_minutes, deadline, remind_at, source_*`.
Creating a child with an existing `(parent_matter_id, step_id)` returns the
existing row (idempotent dispatch). Child creation requires access to the
parent and appends a `child_created` activity on the parent.

`PUT /api/v1/matters/:id` additionally accepts `leader_uid` (reassign →
`assignment_epoch`+1, activity `reassigned`, doorbell to old+new leader),
`mode`, `project_id`, `expected_duration_minutes`.

`GET /api/v1/matters` new filters: `parent_id=<uuid>`, `top_level=1`,
`project_id=<uuid>`, `leader_id=<uid>`. Default behaviour unchanged.

## New matter endpoints

- `GET /api/v1/matters/:id/tree` → skeleton for orchestrators (doc 09 CLI
  `get --tree`):
  ```json
  { "matter": {…}, "mode": "swarm",
    "children": [ {"id","seq_no","title","status","leader_uid","assignees":[],"step_id","step_order"} ],
    "barrier_state": "waiting|ready|merging|joined",
    "join_ready": false, "events_seq": 7, "processed_seq": 5,
    "contract": {"visibility":"blind|shared|upstream|pair","report_to":"leader|next|verifier"} }
  ```
- `GET /api/v1/matters/:id/edges` → outbox-derived Octo orchestration view:
  `{source?, data:[{id,kind,event,state,state_label,target_uid,actor_uid,label,detail,retry_count,next_retry_at,last_error?,created_at,updated_at}]}`.
  This is the first read model for “结构外的力量”: no new ledger table yet,
  but doorbells/homecoming/schedule/watchdog/preference rings are visible as
  human-readable edges. `state` stays the raw outbox state; `state_label` is
  user-facing. Assigned doorbells may upgrade `state_label` from the matter
  lifecycle when the target is still the current leader: `in_progress` → 已开工,
  `review` → 已交回, `done` → 已完成.
- `POST /api/v1/matters/:id/feedback` body
  `{ "content": "哪儿不对怎么改", "entry_id"?: uuid, "anchor"?: {…}, "target_uid"?: uid }`
  → stores H feedback, appends `feedback_added` activity; if matter is in
  `review` the server derives the S transition `review→in_progress` and rings
  the assignee/leader doorbell. Users only (bots 403). Response:
  `{feedback, matter_status}`.
- `GET /api/v1/matters/:id/feedback` → list.
- `POST /api/v1/matters/:id/touch` → refresh `last_activity_at` (non-event,
  no routing). Assignee/leader/creator.
- `POST /api/v1/matters/:id/join` body `{ "processed_seq": 7, "action": "start"|"complete" }`
  → leader merge bookkeeping; if `events_seq > processed_seq` the doorbell is
  re-enqueued (合并必达). `start` appends `join_started` activity.
- `POST /api/v1/matters/:id/summary` → generate Smart-Summary draft via LLM
  (503 `LLM_NOT_CONFIGURED` when no key). Creator only.
- `GET  /api/v1/matters/:id/summary` → latest summary row
  `{id,status:draft|authorized|discarded,content,target_bot_uid,scope,scope_type,scope_key,evidence_matter_id,evidence_entry_ids,evidence_feedback_ids,confidence,hit_count,miss_count,last_applied_at}`.
  Returns `204 No Content` when the matter has no summary/preference draft yet
  (empty state, not 404).
- `PUT  /api/v1/matters/:id/summary/:sid` body
  `{ "action":"authorize"|"discard"|"hit"|"miss", "content"?, "target_bot_uid"?, "scope"?, "scope_type"?, "scope_key"? }`
  — authorize requires `target_bot_uid` ∈ caller's owned bots. `hit`/`miss`
  are only valid on authorized rows and calibrate confidence plus hit/miss
  counters. `scope_type` may be `matter|project|bot|space|global`; when omitted
  the server keeps the old matter scope, and when `scope_key` is omitted the
  server fills the natural key (matter id, project id, bot uid or space id).
  NOTE: writing into agent memory has no real interface yet; rows stop at
  `authorized`.
- `GET /api/v1/matters/:id/preference-hints?limit=5` → recall authorized
  Preference rows that are applicable to the matter's responsible bot.
  Returns `{matter_id,target_bot_uid,preference_context,data:[{summary_id,
  matter_id,scope,scope_type,scope_key,content,confidence,hit_count,miss_count,
  last_applied_at,updated_at,match,match_label}]}`. `match` is one of
  `matter|project|bot|space|global`. `preference_context` is a compact
  bot-readable Markdown block suitable for the executor to add to its own
  prompt/brief; Matter still exposes it as recall data rather than silently
  mutating an agent runtime.
- `PUT /api/v1/matters/:id/preference-hints/:sid` body
  `{action:"hit"|"miss"|"discard"|"scope_matter"}`
  → calibrate, revoke, or narrow a recalled authorized Preference from the receiving matter.
  The server re-checks that the caller can access `:id`, that `:sid` targets
  the matter's responsible bot, and that its scope still matches `:id`. `discard`
  additionally requires caller ownership of the target bot, then removes the
  Preference from future recall by changing the summary status to `discarded`.
  `scope_matter` also requires target-bot ownership and rewrites the Preference
  scope to the current matter only, avoiding future project/space/global recall.
- `GET /api/v1/matters/:id/context` → deferred right-rail context bundle for
  the Matter UI. Returns `{edges:{ok,data|error}, preference_hints:{ok,data|error},
  summary:{ok,data|null|error}}`. Access is checked through the same Matter
  visibility path as `edges`; Preference and summary failures are isolated so a
  missing optional block does not break the detail page.
- `GET /api/v1/bots/:uid/preferences?status=all|authorized|discarded&limit=100`
  → owner-only Preference ledger for one bot. Returns
  `{target_bot_uid,status,stats,data:[{summary_id,matter_id,matter_seq_no,matter_title,target_bot_uid,status,scope,scope_type,scope_key,content,confidence,hit_count,miss_count,last_applied_at,updated_at,duplicate_count?,duplicate_group_key?,duplicate_preferred?,duplicate_reason?}]}`.
  Drafts are excluded; this surface is for managing records that already affect
  recall or were explicitly revoked. `duplicate_count`/`duplicate_group_key`
  are emitted when the same normalized rule content appears multiple times for
  the same target bot, so the owner can clean up redundant Preference rows.
  `duplicate_preferred` marks the suggested row to keep, ranked by authorized
  status, narrower scope, hit/miss health, confidence, and recency.
- `PUT /api/v1/bots/:uid/preferences/:sid` body
  `{action:"restore"|"discard"|"scope_source"}`
  → owner-only ledger action. `restore` moves a discarded Preference back to
  `authorized`; `discard` revokes it from future recall; `scope_source` keeps
  it authorized but rewrites scope to the source matter only.

## Projects (文件夹)

- `POST /api/v1/projects` `{name, description?, scope?, source_channel_id?, source_name?, default_leader_uid?}`
- `GET /api/v1/projects` (space-scoped, `archived=1` to include archived)
- `PUT /api/v1/projects/:id` (creator only; `archived: true|false` toggles)
- matters carry `project_id`; list filter `project_id`.

## Schedules (定时事项 / 自动化, Or5)

- `POST /api/v1/schedules` `{title, runbook?, cron_expr, timezone?, executor_uid, project_id?}`
  — `executor_uid` must be one of the caller's own bots (PRD 鉴权通则).
- `GET /api/v1/schedules` · `PUT /api/v1/schedules/:id` (incl. `enabled`) ·
  `DELETE /api/v1/schedules/:id`
- Runner: due schedule → idempotently creates a matter
  (`schedule_id`+`scheduled_at` unique), leader/assignee = executor bot,
  doorbell to executor. `last_run_at`/`next_run_at` exposed.

## Agent stats (S-derived, AgentCard 赚来半)

`GET /api/v1/agents/stats?uids=a,b,c` →
```json
{ "stats": { "<uid>": { "assigned": 12, "done": 9, "in_review": 2,
              "recent": [ {"matter_id","seq_no","title","done_at"} ] } } }
```
Counts only matters in the caller's space that went through real transitions.

## Doorbells (outbox)

Transitions/dispatch enqueue rows in `matter_outbox` within the same
transaction. A dispatcher loop delivers them through octo-server
`POST /v1/internal/notify` (event `matter.doorbell`, payload carries
`message_key`, `params`, `matter_id`, `seq_no`, `edge`, `url`). Routing table
(doc 02.5): 指派→新负责人; 子交回→父 leader (split/swarm 互盲);
pipeline 段k交回→段k+1 负责人; critic 生成交回→验证方;
父→review→发起人「该你了」; blocked→发起人; 打回→负责人.
Suppression: producer == target ⇒ no doorbell. Retries with exponential
backoff; rows mark `consumed` when the target uid next reads/writes that
matter; undeliverable rows go `dead` and escalate to the creator.

## Watchdog

- `MATTER_WATCHDOG_ENABLED=false` disables automatic revive/block transitions
  for acceptance or demo environments. Production defaults to enabled.
- Revive tier (default every 60s): parents `in_progress` whose non-cancelled
  children are all handed back but no parent transition for
  `MATTER_WATCHDOG_REVIVE_MINUTES` (default 5) → re-ring leader. Leaves
  `in_progress` with no activity past `max(default SLA, expected_duration)`
  → re-ring assignee.
- Blocked tier: still silent `MATTER_WATCHDOG_BLOCK_MINUTES` (default 15)
  after a revive ring → system transition to `blocked` (双措辞 kind=system
  "失联") + doorbell creator.

## Internal API (X-Internal-Token)

Contracts the fleet PoC already codes against (octo-fleet
modules/runtime/bot_task.go):

- `POST /api/v1/internal/matters/:id/timeline` `{actor_uid, space_id, content}`
- `POST /api/v1/internal/matters/:id/activities` `{actor_uid, action, detail}`
  (whitelist: `agent_task_completed`, `agent_task_failed`, `agent_progress`)
- `POST /api/v1/internal/bot-tasks` (fleet createBotTaskReq shape) → queue row
- `POST /api/v1/internal/bot-tasks/:id/ack` `{claim_token, status, result_summary?, error_msg?}`
  → records status, writes the result back into the matter timeline +
  activities, rings creator/leader.
- `POST /api/v1/internal/bot-tasks/claim` `{bot_uids:[], daemon_id?, limit?}`
  → atomically claim queued tasks (`queued→dispatched`, returns
  `claim_token`s). Each task includes `matter_brief` (description,
  `brief_constraints`, `brief_output_spec`, status/epoch metadata),
  `agent_context` (declared AgentCard capabilities), `project_context` (project
  source digest + compact text), and `preference_context` plus
  `preference_hints` for the claimed `bot_uid`, so executors can explicitly
  place capability boundaries, shared context, matched user preferences and
  acceptance constraints into their run brief instead of reimplementing Matter
  scope matching. It also returns `run_context`, a combined text block assembled
  from those same fields for prompt/brief injection. This is the executor pull
  surface; NO executor ships with the local stack yet (see gap list).
- `GET /api/v1/internal/bot-tasks?status=&bot_uid=` → ops listing.

## Prototype-fidelity additions (migration 009)

- Matter Brief: `brief_constraints`, `brief_output_spec` (TEXT, H-mounted —
  doc 02/05 “硬约束+验收 折进 Brief”) accepted on `POST/PUT /matters` and
  returned on every matter payload.
- List filter: `GET /matters?schedule_id=<uuid>` → runs of one automation
  (matters stamped by the schedule runner). Use for “最近运行”.
- Schedules: `output_mode` (`track`=每次运行立成事项 | `runonly`=结果发回会话),
  `target_channel_id`, `target_channel_name`. Delivery of runonly results is
  the EXECUTOR agent's job (O3 report-back) — the schedule doorbell to the
  executor carries `output_mode` / `target_*` / `runbook` in its params; the
  matter service itself never posts into channels (no such internal API,
  see gap list).
- Project sources (共享上下文, H-mounted):
  - `GET    /api/v1/projects/:id/sources` → `{data:[{id,kind,title,ref,snippet,created_by,created_at}]}`
  - `POST   /api/v1/projects/:id/sources` `{kind: chat|file|link, title, ref?, snippet?}`
  - `DELETE /api/v1/projects/:id/sources/:sid` (source author or project creator)
- `GET /agents/stats` per-uid shape grew: `in_progress` count, `current`
  (live matters, ≤5) and `preferences` (authorized smart-summaries targeting
  the uid: `{summary_id, matter_id, scope, scope_type, scope_key, content,
  confidence, hit_count, miss_count, last_applied_at, updated_at}`) — the real
  halves of the AgentCard. `content` is the distilled rule itself so the UI can
  show WHAT the bot learned (人看一眼,不是只看 scope.preference 文件名).
  Declaration-half fields (权限/能力边界/接入) come from `/agent-cards/:uid`.

## octo-server same-origin APIs the UI may call directly

Behind nginx everything shares one origin, so the embedded UI can call
octo-server with the same `token` header:

- `GET /api/v1/space/my` → spaces (auto space discovery)
- `GET /api/v1/space/:id/members?limit=200` → `[{uid,name,role,robot,owner_uid}]` —
  THE uid→display-name map and the assignee/executor picker datasource
  (`robot:1` rows are bots; `owner_uid` = the bot's creator, empty for humans).
  The UI uses `owner_uid == auth.uid` to show 「你创建的」 and unlock the
  agent-card editor — without it the declared-half sovereignty is unreachable.

## Embedded UI

`GET /` and `GET /ui` / `GET /ui/` serve the restored Matter workspace
(single-page, hash routes
`#/inbox  #/mine  #/initiated  #/review-me  #/board  #/projects
#/automation  #/matter/:id  #/project/:id`).
Behind nginx, `GET /matter` redirects to `/matter/ui` with the original
host/port preserved, while `/matter/` and `/matter/ui` serve the SPA directly.
Same-origin auth reuse: reads `localStorage` keys `token`, `uid`, `name`,
`currentSpaceId` written by octo-web; when only the token is present the UI
auto-discovers the space via `/api/v1/space/my`. Manual form otherwise.
`?embed=1` (used by the octo-web sidebar iframe) hides the UI's own leftmost
icon rail — the host app already provides global navigation.

Local verification:

- These scripts hit the same live stack and create temporary fixtures; run them
  serially. They share a local acceptance lock and fail fast if another run is
  active.
- `./scripts/v2-smoke.sh` runs the API/live-stack acceptance loop and static
  embedded-UI contract checks. It defaults to the sibling
  `Code/octo-deployment/docker`; set `DEPLOY_DIR=/path/to/octo-deployment/docker`
  for a nonstandard checkout. Doorbell acceptance uses a fake sink bot by
  default and parks the outbox before dispatcher delivery; set `LIVE_NOTIFY=1`
  only when deliberately testing the octo-server notify path end-to-end against
  a real bot identity.
- `./scripts/v2-cli-cases.sh` runs the real bot-identity CLI acceptance cases:
  delegated single-bot loop, send-back after human feedback, bot-led swarm,
  join fencing, reassignment epoch fencing, bot workspace listing, and
  zero-trace cleanup. It uses a real bot profile and parks fixture doorbells to
  reduce wakeup/token risk, but it can still race a live worker; avoid repeated
  runs when model credit is constrained and override `BOT_UID` / `BOT_PROFILE`
  only intentionally.
- `RUN_LABEL=local node scripts/v2-ui-sweep.mjs` runs an optional Playwright
  sweep for `#/matter/:id`: it creates temporary open / in_progress / review /
  blocked matters, checks desktop and mobile detail pages for console errors,
  page errors, horizontal overflow, unnamed visible buttons, and blocked reason
  visibility, saves screenshots, then deletes the fixtures. If Playwright is not
  installed in this repo, the script tries the sibling `octo-web/apps/web`
  workspace; otherwise set `PLAYWRIGHT_REQUIRE_FROM=/path/to/package.json`.
  Defaults assume the local Octo workspace layout (`Code/octo-matter`,
  `Code/octo-deployment`, `research/`); override `DEPLOY_DIR=...` or
  `OUT_DIR=...` when running elsewhere. Use `KEEP_FIXTURES=1` only when
  intentionally preserving a failing fixture for inspection.

## 2026-06-12 打磨期新增面(均已活体验证)

- `GET /api/v1/matters?seq=N`(别名 `seq_no=N`)— 人说「M-42」,机器查 UUID。
- `GET /api/v1/agent-cards` — 派活名册(全空间能力描述)。
  `GET /api/v1/agent-cards/:uid` — 单卡 `{declared, earned}`;declared=creator 发布的能力描述
  (visibility=private 时对非主人隐藏),earned=验收实算(永远派生,不可造假)。
  Response 还带 `viewer={relationship,can_edit,declared_visible}` 给 IM 头像详情和
  Matter 名片同源分权限展示。declared 支持 `capabilities[]` 机器可读能力条目:
  `{name,description,source,status,homepage,visibility}`;`source=openclaw` 表示来自
  OpenClaw skill 快照,`visibility=owner` 仅 creator 可见。当前 PUT 上限为 60
  条 capabilities,用于避免名片变成完整工具注册表。服务端会把真实
  OpenClaw source 变体(例如 `openclaw-bundled`, `openclaw-extra`,
  `agents-skills-personal`)归一为 `openclaw`。明显敏感的 OpenClaw 能力
  (例如 password/mail/browser/filesystem/shell/Lark/notes 等)服务端会强制
  `visibility=owner`,即使客户端误传 `space`。
  `PUT /api/v1/agent-cards/:uid` — 仅 creator;字段 tagline/description/skills[]/systems[]/capabilities[]/visibility。
- `POST /api/v1/matters/:id/send-back` — 手动把进度发回来源会话(homecoming 队列;
  需 source_channel_id+type 且负责人为 bot,否则诚实 4xx)。
- `GET /api/v1/bots/:uid/channels` — 该 bot 可发言的群(自动化目标选择器;owner 鉴权)。
- `GET /api/v1/bots/:uid/preferences` — 该 bot 的 Preference 管理账本(owner 鉴权),
  用于查看 authorized/discarded 记录与命中/失准健康度。
  `PUT /api/v1/bots/:uid/preferences/:sid` — owner 在账本里恢复/撤销/收窄记录。
- `POST /api/v1/matters/:id/summary` 带 `{content}` — 负责 bot 提交偏好草案(护栏4,
  零服务端 LLM);空体保持原 LLM 生成路径。authorize/discard 后以
  `matter.doorbell.summary_approved/rejected` 回铃提交方;hit/miss 只校准记录,
  不发回铃。
- 门铃事件新增:`matter.homecoming`(回源会话投递,target=发声 bot,豁免消费钩子与防自激)、
  `matter.doorbell.reflect`(验收时有圈点且负责人为 bot → 偏好沉淀提示)、
  `matter.project.context_added`(项目共享上下文变更 → 默认负责人)。
- timeline `msgs[]` 路径:LLM 缺席/故障时**无损降级**为逐条引用入档(来源=聊天记录引用),
  LLM 可用时自动升级为摘要;extract 建单路径维持诚实报错不降级。
- 行为修正:门铃消费=调用者本人(主人围观不再消押 agent 的铃);软删事项停其全部活铃;
  已投未消费重敲按指数退避(10m·2^n,封顶 2^5);bot 自指 uid 大小写按 auth 实名矫正;
  bot 带 source_channel_id 创建时默认 channel_type=2。
