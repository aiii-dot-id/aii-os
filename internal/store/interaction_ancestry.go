package store

import "context"

const interactionParentCTE = `WITH RECURSIVE requests AS (
 SELECT source, dedup_key,
        json_extract(CASE WHEN json_valid(payload) THEN payload ELSE '{}' END,'$.ws_id') AS child,
        json_extract(CASE WHEN json_valid(payload) THEN payload ELSE '{}' END,'$.parent_work_session_id') AS parent
 FROM work_queue WHERE kind='subagent.run'
), parents AS (
 SELECT child, MIN(parent) AS parent FROM requests
 GROUP BY child
 HAVING COUNT(DISTINCT parent)=1
 AND MIN(CASE WHEN source='identity' AND typeof(child)='text' AND child!=''
              AND typeof(parent)='text' AND parent!='' AND parent!=child
              AND (dedup_key=child OR substr(dedup_key,1,length(child)+1)=child||'#')
              AND EXISTS(SELECT 1 FROM work_sessions WHERE id=child)
              AND EXISTS(SELECT 1 FROM work_sessions WHERE id=parent)
         THEN 1 ELSE 0 END)=1
), lineage(child,ancestor) AS (
 SELECT child,parent FROM parents
 UNION SELECT l.child,p.parent FROM lineage l JOIN parents p ON p.child=l.ancestor
), cyclic(id) AS (
 SELECT child FROM lineage WHERE child=ancestor
), proven_parents AS (
 SELECT child,parent FROM parents
 WHERE child NOT IN (SELECT id FROM cyclic) AND parent NOT IN (SELECT id FROM cyclic)
)`

func interactionDescendants(ctx context.Context, tx *readSnapshot, root string) (predicate string, complete bool, err error) {
	predicate = "json_extract(c.details,'$.work_session_id') = ?"
	var tables int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('work_queue','work_sessions')`).Scan(&tables); err != nil || tables != 2 {
		return predicate, false, err
	}
	if err = tx.QueryRowContext(ctx, interactionParentCTE+`
 SELECT EXISTS(SELECT 1 FROM work_sessions WHERE id=?)
 AND NOT EXISTS(SELECT 1 FROM requests r WHERE r.child IS NULL OR NOT EXISTS(SELECT 1 FROM proven_parents p WHERE p.child=r.child))`, root).Scan(&complete); err != nil {
		return "", false, err
	}
	predicate = "json_extract(c.details,'$.work_session_id') IN (" + interactionParentCTE + `,
 descendants(id) AS (SELECT ? UNION SELECT p.child FROM proven_parents p JOIN descendants d ON p.parent=d.id)
 SELECT id FROM descendants)`
	return predicate, complete, nil
}
