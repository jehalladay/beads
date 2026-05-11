REPLACE INTO dolt_ignore VALUES ('__temp_wisp_comments', true);
ALTER TABLE wisp_comments RENAME TO __temp_wisp_comments;
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
INSERT INTO wisp_comments SELECT * FROM __temp_wisp_comments;
DROP TABLE __temp_wisp_comments;
DELETE FROM dolt_ignore WHERE pattern = '__temp_wisp_comments';
