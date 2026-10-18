package store

type PluginMemorySelection struct{ Kind, Project, WorkSession string }

func (s PluginMemorySelection) Predicate() (string, []any) {
	switch s.Kind {
	case "":
		return "1", nil
	case "legacy", "reusable":
		return "b.applicability = ?", []any{s.Kind}
	case "task":
		return "b.applicability = 'task' AND b.work_session = ? AND b.project = ? AND ? != ''", []any{s.WorkSession, s.Project, s.WorkSession}
	case "project":
		return "b.applicability = 'project' AND b.project = ? AND ? != ''", []any{s.Project, s.Project}
	case "relevant":
		return "(b.applicability = 'reusable' OR (b.applicability = 'project' AND b.project = ? AND ? != '') OR (b.applicability = 'task' AND b.work_session = ? AND b.project = ? AND ? != ''))", []any{s.Project, s.Project, s.WorkSession, s.Project, s.WorkSession}
	default:
		return "0", nil
	}
}

func (s *Store) PluginMemorySelected(id string, selection PluginMemorySelection) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	where, args := selection.Predicate()
	var yes bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM plugin_memories b WHERE b.id = ? AND ("+where+"))", append([]any{id}, args...)...).Scan(&yes)
	return yes, err
}

func pluginMemoryEquivalent(tx dbi, scope PluginScope, m PluginMemory) (string, error) {
	where, args := PluginMemoryPredicate(scope, true)
	selectWhere, selectArgs := (PluginMemorySelection{Kind: m.Applicability, Project: m.Project, WorkSession: m.WorkSession}).Predicate()
	args = append(args, selectArgs...)
	args = append(args, m.Text, m.Evidence)
	var id string
	err := tx.QueryRow("SELECT COALESCE((SELECT b.id FROM plugin_memories b WHERE "+where+" AND ("+selectWhere+") AND b.text = ? AND b.evidence = ? ORDER BY b.id LIMIT 1),'')", args...).Scan(&id)
	return id, err
}

func pluginMemorySuccessor(scope PluginScope) (string, []any) {
	return `COALESCE(
		(SELECT root.superseded_by FROM plugin_memories root
		 WHERE b.temp = 1 AND root.id = b.overrides AND root.plugin_id = b.plugin_id),
		b.superseded_by,
		(SELECT local.id FROM plugin_memories local
		 WHERE b.temp = 0 AND local.plugin_id = b.plugin_id AND local.temp = 1
		 AND local.activation = ? AND local.overrides = b.id AND local.superseded_by IS NULL))`, []any{scope.Activation}
}

func PluginMemoryPredicate(scope PluginScope, current bool) (string, []any) {
	where := `b.plugin_id = ? AND (b.temp = 0 OR b.activation = ?)`
	args := []any{scope.PluginID, scope.Activation}
	if current {
		successor, successorArgs := pluginMemorySuccessor(scope)
		where += ` AND ` + successor + ` IS NULL`
		args = append(args, successorArgs...)
	}
	return where, args
}
