REPLACE INTO dolt_ignore VALUES ('__temp_wisp_events', true);
-- Rename only when the legacy wisp_events table is present (see migration 0040).
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_events') > 0,
    'ALTER TABLE wisp_events RENAME TO __temp_wisp_events',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
CREATE TABLE wisp_events (
    id CHAR(36) NOT NULL PRIMARY KEY DEFAULT (UUID()),
    issue_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(32) NOT NULL,
    actor VARCHAR(255) DEFAULT '',
    old_value TEXT DEFAULT '',
    new_value TEXT DEFAULT '',
    comment TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_wisp_events_issue (issue_id),
    INDEX idx_wisp_events_created_at (created_at)
);
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('wisp_events', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table wisp_events');
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '__temp_wisp_events') > 0,
    'INSERT INTO wisp_events SELECT * FROM __temp_wisp_events',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
DROP TABLE IF EXISTS __temp_wisp_events;
DELETE FROM dolt_ignore WHERE pattern = '__temp_wisp_events';
