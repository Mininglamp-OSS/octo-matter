-- Dev-only seed for Octo Matter verification.
-- Run after migrations, against the octo_matter database:
-- mysql -uroot -p octo_matter --default-character-set=utf8mb4 < octo-matter/scripts/seed-demo.sql

SET NAMES utf8mb4;
SET collation_connection = 'utf8mb4_unicode_ci';

SET @space_id = 'space-demo-octo';
SET @project_id = '1b355ca0-1687-4561-ad0b-e56b86d5a7ff';
SET @matter_delivery = '69a59e1d-5448-4cd5-ad1e-4cc744d5ef0d';
SET @matter_docs = '04af0a3f-d248-4735-ba91-d9336d07442e';
SET @product_group = 'grp_product_docs';
SET @delivery_group = 'grp_delivery_docs';
SET @policy_group = 'grp_policy_docs';

DELETE FROM matter_timeline_attachments
WHERE matter_id IN (@matter_delivery, @matter_docs)
   OR entry_id IN ('TL-DEMO-DELIVERY-001', 'TL-DEMO-DOCS-001', 'TL-DEMO-DOCS-002');

DELETE FROM matter_timelines
WHERE matter_id IN (@matter_delivery, @matter_docs)
   OR id IN ('TL-DEMO-DELIVERY-001', 'TL-DEMO-DOCS-001', 'TL-DEMO-DOCS-002');

DELETE FROM matter_activities
WHERE matter_id IN (@matter_delivery, @matter_docs);

DELETE FROM matter_channels
WHERE matter_id IN (@matter_delivery, @matter_docs);

DELETE FROM matter_assignees
WHERE matter_id IN (@matter_delivery, @matter_docs);

DELETE FROM matter_participants
WHERE matter_id IN (@matter_delivery, @matter_docs);

DELETE FROM matters
WHERE id IN (@matter_delivery, @matter_docs);

DELETE FROM matter_project_sources
WHERE project_id = @project_id;

DELETE FROM matter_projects
WHERE id = @project_id;

INSERT INTO matter_projects
  (id, space_id, name, description, scope, source_channel_id, source_name, default_leader_uid, creator_id, archived, created_at, updated_at)
VALUES
  (@project_id, @space_id, 'Octo 文档治理验收项目', '验证群聊文件、文档空间、事项附件和产出文件的端到端闭环。', 'space', @product_group, '产品方案讨论群', 'pm_chen', 'pm_chen', 0, '2026-06-17 09:00:00.000', NOW(3))
ON DUPLICATE KEY UPDATE
  name = VALUES(name),
  description = VALUES(description),
  source_channel_id = VALUES(source_channel_id),
  source_name = VALUES(source_name),
  default_leader_uid = VALUES(default_leader_uid),
  archived = 0,
  updated_at = NOW(3);

INSERT INTO matter_project_sources
  (id, project_id, space_id, kind, title, ref, snippet, created_by, created_at)
VALUES
  ('SRC-DEMO-PRODUCT', @project_id, @space_id, 'chat', '产品方案讨论群', @product_group, '产品需求、竞品资料和评审结论沉淀在产品部公共空间。', 'pm_chen', '2026-06-17 09:05:00.000'),
  ('SRC-DEMO-DELIVERY', @project_id, @space_id, 'chat', '华东项目交付群', @delivery_group, '客户交付材料和现场实施计划沉淀在华东交付空间。', 'delivery_liu', '2026-06-17 09:06:00.000'),
  ('SRC-DEMO-POLICY', @project_id, @space_id, 'chat', '行政制度发布群', @policy_group, '公司级正式制度和公告沉淀在公司制度空间。', 'admin_zhou', '2026-06-17 09:07:00.000');

INSERT INTO matters
  (
    id, seq_no, space_id, parent_matter_id, title, description,
    brief_constraints, brief_output_spec, creator_id, leader_uid, status,
    mode, step_id, step_order, project_id, assignment_epoch, version,
    events_seq, processed_seq, inflight, expected_duration_minutes,
    last_activity_at, last_transition_at, deadline, sort_order, remind_at,
    source_channel_id, source_channel_type, source_name, source_msg_ids,
    input_attachments, created_at, updated_at, deleted_at
  )
VALUES
  (
    @matter_delivery, 1, @space_id, NULL, '整理华东交付材料清单',
    '从华东项目交付群沉淀客户现场计划、权限确认截图和会议纪要。',
    '只使用华东项目交付群和文档中心中已归档的客户交付材料。',
    '输出一份可直接用于项目周会的材料清单，附件必须能从事项产出页查看。',
    'pm_chen', 'delivery_liu', 'open',
    'solo', NULL, NULL, @project_id, 0, 1,
    0, 0, 0, 120,
    NOW(3), '2026-06-17 09:35:00.000', NULL, 1000, NULL,
    @delivery_group, 2, '华东项目交付群', JSON_ARRAY('msg-demo-1001', 'msg-demo-1004'),
    JSON_ARRAY(
      JSON_OBJECT(
        'file_url', 'common/documents/demo/q3-delivery-plan.pdf',
        'file_name', 'Q3 客户现场实施计划.pdf',
        'file_size', 18400000,
        'mime_type', 'application/pdf'
      ),
      JSON_OBJECT(
        'file_url', 'common/documents/demo/account-confirm.png',
        'file_name', '客户账号权限确认截图.png',
        'file_size', 820000,
        'mime_type', 'image/png'
      )
    ),
    '2026-06-17 09:35:00.000', NOW(3), NULL
  ),
  (
    @matter_docs, 2, @space_id, NULL, '补齐文档中心上传下载验收',
    '验证直接上传、在线预览、下载计数、回收站恢复和来源会话跳转。',
    '优先覆盖产品方案讨论群、行政制度发布群和直接上传文件。',
    '形成一份文档中心上线验收说明，明确哪些文件可预览、哪些必须下载。',
    'pm_chen', 'pm_chen', 'in_progress',
    'solo', NULL, NULL, @project_id, 0, 1,
    0, 0, 0, 180,
    NOW(3), NOW(3), NULL, 900, NULL,
    @product_group, 2, '产品方案讨论群', JSON_ARRAY('msg-demo-1002', 'msg-demo-1003'),
    JSON_ARRAY(
      JSON_OBJECT(
        'file_url', 'common/documents/demo/octo-file-requirements.xlsx',
        'file_name', 'Octo 文件空间需求清单.xlsx',
        'file_size', 2100000,
        'mime_type', 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'
      ),
      JSON_OBJECT(
        'file_url', 'common/documents/demo/policy-update.docx',
        'file_name', '制度更新说明.docx',
        'file_size', 1600000,
        'mime_type', 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'
      )
    ),
    '2026-06-17 09:45:00.000', NOW(3), NULL
  );

INSERT INTO matter_assignees
  (id, matter_id, user_id, created_at)
VALUES
  ('ASG-DEMO-DELIVERY-001', @matter_delivery, 'pm_chen', '2026-06-17 09:36:00.000'),
  ('ASG-DEMO-DELIVERY-002', @matter_delivery, 'delivery_liu', '2026-06-17 09:36:00.000'),
  ('ASG-DEMO-DOCS-001', @matter_docs, 'pm_chen', '2026-06-17 09:46:00.000'),
  ('ASG-DEMO-DOCS-002', @matter_docs, 'admin_zhou', '2026-06-17 09:46:00.000')
ON DUPLICATE KEY UPDATE
  user_id = VALUES(user_id);

INSERT INTO matter_channels
  (id, matter_id, channel_id, channel_type, channel_name, linked_by, created_at)
VALUES
  ('CH-DEMO-DELIVERY-001', @matter_delivery, @delivery_group, 2, '华东项目交付群', 'pm_chen', '2026-06-17 09:37:00.000'),
  ('CH-DEMO-DOCS-001', @matter_docs, @product_group, 2, '产品方案讨论群', 'pm_chen', '2026-06-17 09:47:00.000'),
  ('CH-DEMO-DOCS-002', @matter_docs, @policy_group, 2, '行政制度发布群', 'admin_zhou', '2026-06-17 09:48:00.000')
ON DUPLICATE KEY UPDATE
  channel_name = VALUES(channel_name),
  linked_by = VALUES(linked_by);

INSERT INTO matter_timelines
  (id, matter_id, user_id, on_behalf_of, content, created_at, channel_id, channel_type, source_channel_id, source_msgs, related_uids)
VALUES
  (
    'TL-DEMO-DELIVERY-001', @matter_delivery, 'delivery_liu', NULL,
    '已补充客户现场实施计划和账号权限截图，材料可进入文档中心华东交付空间长期沉淀。',
    '2026-06-17 10:20:00.000', 'CH-DEMO-DELIVERY-001', 2, @delivery_group,
    JSON_ARRAY('msg-demo-1001', 'msg-demo-1004'), JSON_ARRAY('pm_chen', 'delivery_liu')
  ),
  (
    'TL-DEMO-DOCS-001', @matter_docs, 'pm_chen', NULL,
    '产品方案讨论群中的需求清单已归档到产品部公共空间，并进入文档中心验收事项。',
    '2026-06-17 10:25:00.000', 'CH-DEMO-DOCS-001', 2, @product_group,
    JSON_ARRAY('msg-demo-1002'), JSON_ARRAY('pm_chen', 'admin_zhou')
  ),
  (
    'TL-DEMO-DOCS-002', @matter_docs, 'admin_zhou', NULL,
    '行政制度发布群中的制度更新说明已同步，验证非同源群文件在产出页按真实来源展示。',
    '2026-06-17 10:26:00.000', 'CH-DEMO-DOCS-002', 2, @policy_group,
    JSON_ARRAY('msg-demo-1003'), JSON_ARRAY('pm_chen', 'admin_zhou')
  );

INSERT INTO matter_timeline_attachments
  (id, entry_id, matter_id, file_url, file_name, file_size, mime_type, description, sender_uid, sender_uname, sent_at, created_at)
VALUES
  ('ATT-DEMO-DELIVERY-001', 'TL-DEMO-DELIVERY-001', @matter_delivery, 'common/documents/demo/q3-delivery-plan.pdf', 'Q3 客户现场实施计划.pdf', 18400000, 'application/pdf', '客户现场实施计划，可在文档中心归档后持续复用。', 'delivery_liu', '刘青', '2026-06-17 10:18:00.000', '2026-06-17 10:20:00.000'),
  ('ATT-DEMO-DELIVERY-002', 'TL-DEMO-DELIVERY-001', @matter_delivery, 'common/documents/demo/account-confirm.png', '客户账号权限确认截图.png', 820000, 'image/png', '会话内发送的截图，尚未归档前仍属于会话文件。', 'delivery_liu', '刘青', '2026-06-17 10:19:00.000', '2026-06-17 10:20:00.000'),
  ('ATT-DEMO-DOCS-001', 'TL-DEMO-DOCS-001', @matter_docs, 'common/documents/demo/octo-file-requirements.xlsx', 'Octo 文件空间需求清单.xlsx', 2100000, 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', '产品方案讨论群内沉淀的文档中心需求清单。', 'pm_chen', '陈一', '2026-06-17 10:23:00.000', '2026-06-17 10:25:00.000'),
  ('ATT-DEMO-DOCS-002', 'TL-DEMO-DOCS-002', @matter_docs, 'common/documents/demo/policy-update.docx', '制度更新说明.docx', 1600000, 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', '行政制度发布群同步的正式制度文档。', 'admin_zhou', '周岚', '2026-06-17 10:24:00.000', '2026-06-17 10:26:00.000');

INSERT INTO matter_activities
  (id, matter_id, actor_id, action, detail, created_at)
VALUES
  ('ACT-DEMO-DELIVERY-001', @matter_delivery, 'pm_chen', 'created', JSON_OBJECT('source_channel_id', @delivery_group, 'source_name', '华东项目交付群'), '2026-06-17 09:35:00.000'),
  ('ACT-DEMO-DELIVERY-002', @matter_delivery, 'delivery_liu', 'output_added', JSON_OBJECT('files', JSON_ARRAY('Q3 客户现场实施计划.pdf', '客户账号权限确认截图.png')), '2026-06-17 10:20:00.000'),
  ('ACT-DEMO-DOCS-001', @matter_docs, 'pm_chen', 'created', JSON_OBJECT('source_channel_id', @product_group, 'source_name', '产品方案讨论群'), '2026-06-17 09:45:00.000'),
  ('ACT-DEMO-DOCS-002', @matter_docs, 'pm_chen', 'output_added', JSON_OBJECT('files', JSON_ARRAY('Octo 文件空间需求清单.xlsx', '制度更新说明.docx')), '2026-06-17 10:25:00.000');

SELECT
  'matter demo seed ready' AS result,
  @space_id AS space_id,
  (SELECT COUNT(*) FROM matter_projects WHERE space_id = @space_id AND id = @project_id) AS projects,
  (SELECT COUNT(*) FROM matters WHERE space_id = @space_id AND id IN (@matter_delivery, @matter_docs)) AS matters,
  (SELECT COUNT(*) FROM matter_timelines WHERE matter_id IN (@matter_delivery, @matter_docs)) AS timelines,
  (SELECT COUNT(*) FROM matter_timeline_attachments WHERE matter_id IN (@matter_delivery, @matter_docs)) AS attachments;
