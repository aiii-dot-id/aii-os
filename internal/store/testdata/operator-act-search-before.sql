-- Historical searchable-conversation view and sidecar triggers.
CREATE VIEW IF NOT EXISTS conversations_searchable AS
    SELECT rowid AS rowid, id, content
    FROM conversations
    WHERE role IN ('resident', 'operator', 'participant') AND kind IN ('message','legacy') AND source_id IS NULL;

CREATE TRIGGER IF NOT EXISTS conversations_sidecar_ai AFTER INSERT ON conversations WHEN new.role IN ('resident', 'operator', 'participant') AND new.kind IN ('message','legacy') AND new.source_id IS NULL BEGIN
    INSERT INTO conversations_fts(rowid, content) VALUES (new.rowid, new.content);
    INSERT INTO conversations_tri(rowid, content) VALUES (new.rowid, new.content);
END;

CREATE TRIGGER IF NOT EXISTS conversations_sidecar_ad AFTER DELETE ON conversations WHEN old.role IN ('resident', 'operator', 'participant') AND old.kind IN ('message','legacy') AND old.source_id IS NULL BEGIN
    INSERT INTO conversations_fts(conversations_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
    INSERT INTO conversations_tri(conversations_tri, rowid, content) VALUES ('delete', old.rowid, old.content);
END;

CREATE TRIGGER IF NOT EXISTS conversations_sidecar_au AFTER UPDATE OF content, role, kind, source_id ON conversations BEGIN
    INSERT INTO conversations_fts(conversations_fts, rowid, content) SELECT 'delete', old.rowid, old.content WHERE old.role IN ('resident', 'operator', 'participant') AND old.kind IN ('message','legacy') AND old.source_id IS NULL;
    INSERT INTO conversations_tri(conversations_tri, rowid, content) SELECT 'delete', old.rowid, old.content WHERE old.role IN ('resident', 'operator', 'participant') AND old.kind IN ('message','legacy') AND old.source_id IS NULL;
    INSERT INTO conversations_fts(rowid, content) SELECT new.rowid, new.content WHERE new.role IN ('resident', 'operator', 'participant') AND new.kind IN ('message','legacy') AND new.source_id IS NULL;
    INSERT INTO conversations_tri(rowid, content) SELECT new.rowid, new.content WHERE new.role IN ('resident', 'operator', 'participant') AND new.kind IN ('message','legacy') AND new.source_id IS NULL;
END;
