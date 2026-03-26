package base

// SafeSchemaProperties extracts the "properties" field from a JSON Schema map.
// Returns an empty map if schema is nil or "properties" is missing.
func SafeSchemaProperties(schema map[string]any) any {
	if schema == nil {
		return map[string]any{}
	}
	if props, ok := schema["properties"]; ok && props != nil {
		return props
	}
	return map[string]any{}
}

// SafeSchemaRequired extracts the "required" field from a JSON Schema map.
// Returns nil if schema is nil or "required" is missing.
func SafeSchemaRequired(schema map[string]any) []string {
	if schema == nil {
		return nil
	}
	return ToStringSlice(schema["required"])
}
