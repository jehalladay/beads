REPLACE INTO dolt_ignore VALUES ('__temp_wisp_labels', true);
-- Rename only when the legacy wisp_labels table is present (see migration 0040).
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'wisp_labels') > 0,
    'ALTER TABLE wisp_labels RENAME TO __temp_wisp_labels',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
CREATE TABLE wisp_labels (
    issue_id VARCHAR(255) NOT NULL,
    label VARCHAR(255) NOT NULL,
    PRIMARY KEY (issue_id, label),
    INDEX idx_wisp_labels_label (label)
);
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('wisp_labels', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table wisp_labels');
SET @sql = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '__temp_wisp_labels') > 0,
    'INSERT INTO wisp_labels SELECT * FROM __temp_wisp_labels',
    'SELECT 1'
);
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
DROP TABLE IF EXISTS __temp_wisp_labels;
DELETE FROM dolt_ignore WHERE pattern = '__temp_wisp_labels';
