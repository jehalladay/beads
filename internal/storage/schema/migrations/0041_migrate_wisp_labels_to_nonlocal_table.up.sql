REPLACE INTO dolt_ignore VALUES ('__temp_wisp_labels', true);
ALTER TABLE wisp_labels RENAME TO __temp_wisp_labels;
CREATE TABLE wisp_labels (
    issue_id VARCHAR(255) NOT NULL,
    label VARCHAR(255) NOT NULL,
    PRIMARY KEY (issue_id, label),
    INDEX idx_wisp_labels_label (label)
);
INSERT INTO dolt_nonlocal_tables (table_name, target_ref, options) VALUES ('wisp_labels', 'main', 'immediate');
CALL DOLT_COMMIT('-Am', 'create nonlocal table wisp_labels');
INSERT INTO wisp_labels SELECT * FROM __temp_wisp_labels;
DROP TABLE __temp_wisp_labels;
DELETE FROM dolt_ignore WHERE pattern = '__temp_wisp_labels';
