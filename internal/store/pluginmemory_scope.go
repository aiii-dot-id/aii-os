package store

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
