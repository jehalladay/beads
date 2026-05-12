-- Each INSERT/UPDATE/DELETE is guarded by wisp-table existence. Legacy clones
-- may not have the wisp tables in the working set since they used to be
-- dolt-ignored and didn't transfer via DOLT_CLONE. Each DELETE only fires when
-- its corresponding wisp_X table exists (i.e. the row was actually copied),
-- otherwise the originals are left in place to avoid data loss.

SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisps') > 0,
    'INSERT IGNORE INTO wisps SELECT * FROM issues WHERE issue_type IN (''agent'', ''rig'', ''role'', ''message'')',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisps') > 0,
    'UPDATE wisps SET ephemeral = 1 WHERE issue_type IN (''agent'', ''rig'', ''role'', ''message'')',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_labels') > 0,
    'INSERT IGNORE INTO wisp_labels (issue_id, label) SELECT l.issue_id, l.label FROM labels l JOIN issues i ON i.id = l.issue_id WHERE i.issue_type IN (''agent'', ''rig'', ''role'', ''message'')',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_dependencies') > 0,
    'INSERT IGNORE INTO wisp_dependencies (issue_id, depends_on_id, type, created_at, created_by, metadata, thread_id) SELECT d.issue_id, d.depends_on_id, d.type, d.created_at, d.created_by, d.metadata, d.thread_id FROM dependencies d JOIN issues i ON i.id = d.issue_id WHERE i.issue_type IN (''agent'', ''rig'', ''role'', ''message'')',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_events') > 0,
    'INSERT IGNORE INTO wisp_events (id, issue_id, event_type, actor, old_value, new_value, comment, created_at) SELECT e.id, e.issue_id, e.event_type, e.actor, e.old_value, e.new_value, e.comment, e.created_at FROM events e JOIN issues i ON i.id = e.issue_id WHERE i.issue_type IN (''agent'', ''rig'', ''role'', ''message'')',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_comments') > 0,
    'INSERT IGNORE INTO wisp_comments (id, issue_id, author, text, created_at) SELECT c.id, c.issue_id, c.author, c.text, c.created_at FROM comments c JOIN issues i ON i.id = c.issue_id WHERE i.issue_type IN (''agent'', ''rig'', ''role'', ''message'')',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Delete originals, children first (FK-safe order). Each DELETE is gated by
-- the corresponding wisp_X table — skipped on legacy clones where the copy
-- above was a no-op.
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_comments') > 0,
    'DELETE c FROM comments c JOIN issues i ON i.id = c.issue_id WHERE i.issue_type IN (''agent'', ''rig'', ''role'', ''message'')',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_events') > 0,
    'DELETE e FROM events e JOIN issues i ON i.id = e.issue_id WHERE i.issue_type IN (''agent'', ''rig'', ''role'', ''message'')',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_dependencies') > 0,
    'DELETE d FROM dependencies d JOIN issues i ON i.id = d.issue_id WHERE i.issue_type IN (''agent'', ''rig'', ''role'', ''message'')',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_labels') > 0,
    'DELETE l FROM labels l JOIN issues i ON i.id = l.issue_id WHERE i.issue_type IN (''agent'', ''rig'', ''role'', ''message'')',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisps') > 0,
    'DELETE FROM issues WHERE issue_type IN (''agent'', ''rig'', ''role'', ''message'')',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
