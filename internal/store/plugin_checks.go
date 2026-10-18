package store

const pluginChecksKey = "plugin_checks"

func (s *Store) PluginCheckVerdicts() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, err := s.getRuntimeMeta(pluginChecksKey)
	if err != nil || v == "" {
		return nil, err
	}
	return []byte(v), nil
}

func (s *Store) SetPluginCheckVerdicts(raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return setRuntimeMeta(s.w(), pluginChecksKey, string(raw))
}
