package conversation

// configuredHeaders is what a provider entry's providers.json headers give a
// client of that dialect: no vendor header value is compiled in (the
// operator, 2026-11-11), so an Anthropic fixture is configured with its API
// version the way a real entry is. Other dialects need none here.
func configuredHeaders(provider string) map[string]string {
	if provider == "anthropic" {
		return map[string]string{"anthropic-version": "2023-06-01"}
	}
	return nil
}
