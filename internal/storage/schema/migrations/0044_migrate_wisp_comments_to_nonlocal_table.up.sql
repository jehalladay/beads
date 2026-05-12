REPLACE INTO dolt_ignore VALUES ('__temp_wisp_comments', true);
-- Rename only when the legacy wisp_comments table is present (see migration 0040).
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_comments') > 0,
    'ALTER TABLE wisp_comments RENAME TO __temp_wisp_comments',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
CREATE TABLE wisp_comments (
    id CHAR(36) NOT NULL PRIMARY KEY DEFAULT (UUID()),
    issue_id VARCHAR(255) NOT NULL,
    author VARCHAR(255) DEFAULT '',
    text TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_wisp_comments_issue (issue_id)
);
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('wisp_comments', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table wisp_comments');
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '__temp_wisp_comments') > 0,
    'INSERT INTO wisp_comments SELECT * FROM __temp_wisp_comments',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
DROP TABLE IF EXISTS __temp_wisp_comments;
DELETE FROM dolt_ignore WHERE pattern = '__temp_wisp_comments';
DELETE FROM dolt_ignore WHERE pattern = 'wisps_%';
DELETE FROM dolt_ignore WHERE pattern = 'wisp_%';
