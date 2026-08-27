/* One backend-owned provider model list. */
export function providerModels(provider) {
  return ((provider && provider.models) || []).filter(Boolean);
}

export function fillModelPicker(select, provider, preferred) {
  const models = providerModels(provider);
  const current = preferred == null ? ((provider && provider.default_model) || '') : preferred;
  if (current && !models.includes(current)) models.unshift(current);
  select.replaceChildren();
  for (const value of models) {
    const option = document.createElement('option');
    option.value = value;
    option.textContent = value;
    select.appendChild(option);
  }
  select.value = current;
}
