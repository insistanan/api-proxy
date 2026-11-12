package providers

// Gemini 函数声明的 JSON Schema 归一化：Claude/OpenAI 风格 schema -> Gemini 接受的形态。
// 全部为纯函数，不依赖任何外部包。

func normalizeGeminiJSONSchema(schema interface{}) interface{} {
	if schema == nil {
		return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
	}
	normalized := normalizeGeminiJSONSchemaValue(schema)
	if schemaMap, ok := normalized.(map[string]interface{}); ok {
		if _, hasType := schemaMap["type"]; !hasType {
			schemaMap["type"] = "object"
		}
		if schemaMap["type"] == "object" {
			if _, hasProps := schemaMap["properties"]; !hasProps {
				schemaMap["properties"] = map[string]interface{}{}
			}
		}
		return schemaMap
	}
	return normalized
}

func normalizeGeminiJSONSchemaValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, raw := range v {
			if key == "$schema" || key == "$id" {
				continue
			}
			switch key {
			case "properties":
				if props, ok := raw.(map[string]interface{}); ok {
					cleanedProps := make(map[string]interface{}, len(props))
					for propName, propSchema := range props {
						cleanedProps[propName] = normalizeGeminiJSONSchemaValue(propSchema)
					}
					out[key] = cleanedProps
					continue
				}
			case "items", "not", "if", "then", "else", "additionalProperties":
				out[key] = normalizeGeminiJSONSchemaValue(raw)
				continue
			case "anyOf", "oneOf", "allOf", "prefixItems", "any_of", "one_of", "all_of", "prefix_items":
				if arr, ok := raw.([]interface{}); ok {
					cleaned := make([]interface{}, 0, len(arr))
					for _, item := range arr {
						cleaned = append(cleaned, normalizeGeminiJSONSchemaValue(item))
					}
					out[normalizeGeminiSchemaKeyword(key)] = cleaned
					continue
				}
			}
			out[normalizeGeminiSchemaKeyword(key)] = normalizeGeminiJSONSchemaValue(raw)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i := range v {
			out[i] = normalizeGeminiJSONSchemaValue(v[i])
		}
		return out
	default:
		return v
	}
}

func normalizeGeminiSchemaKeyword(key string) string {
	switch key {
	case "any_of":
		return "anyOf"
	case "one_of":
		return "oneOf"
	case "all_of":
		return "allOf"
	case "prefix_items":
		return "prefixItems"
	case "property_names":
		return "propertyNames"
	case "exclusive_minimum":
		return "exclusiveMinimum"
	case "exclusive_maximum":
		return "exclusiveMaximum"
	case "multiple_of":
		return "multipleOf"
	default:
		return key
	}
}

func requiresGeminiParametersJSONSchema(schema interface{}) bool {
	switch v := schema.(type) {
	case map[string]interface{}:
		return geminiSchemaObjectRequiresJSONSchema(v)
	case []interface{}:
		for _, item := range v {
			if requiresGeminiParametersJSONSchema(item) {
				return true
			}
		}
	}
	return false
}

func geminiSchemaObjectRequiresJSONSchema(obj map[string]interface{}) bool {
	for key, value := range obj {
		switch key {
		case "type":
			switch value.(type) {
			case []interface{}, []string:
				return true
			}
		case "format", "title", "description", "nullable", "enum",
			"maxItems", "minItems", "required", "minProperties", "maxProperties",
			"minLength", "maxLength", "pattern", "example", "propertyOrdering",
			"default", "minimum", "maximum":
		case "properties":
			props, ok := value.(map[string]interface{})
			if !ok {
				return true
			}
			for _, propSchema := range props {
				if requiresGeminiParametersJSONSchema(propSchema) {
					return true
				}
			}
		case "items":
			if _, ok := value.(map[string]interface{}); !ok {
				return true
			}
			if requiresGeminiParametersJSONSchema(value) {
				return true
			}
		case "anyOf":
			values, ok := value.([]interface{})
			if !ok {
				return true
			}
			for _, item := range values {
				if requiresGeminiParametersJSONSchema(item) {
					return true
				}
			}
		case "$ref", "$defs", "definitions",
			"additionalProperties", "unevaluatedProperties", "propertyNames", "patternProperties",
			"oneOf", "allOf", "const", "not", "if", "then", "else",
			"dependentRequired", "dependentSchemas", "contains", "minContains", "maxContains",
			"prefixItems", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "examples":
			return true
		default:
			return true
		}
	}
	return false
}

func toGeminiFunctionParametersSchema(schema interface{}) map[string]interface{} {
	defaultSchema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
	schemaMap, ok := schema.(map[string]interface{})
	if !ok {
		return defaultSchema
	}
	out := make(map[string]interface{}, len(schemaMap))
	for key, value := range schemaMap {
		switch key {
		case "type", "format", "title", "description", "nullable", "enum",
			"maxItems", "minItems", "required", "minProperties", "maxProperties",
			"minLength", "maxLength", "pattern", "example", "propertyOrdering",
			"default", "minimum", "maximum":
			out[key] = value
		case "properties":
			if props, ok := value.(map[string]interface{}); ok {
				converted := make(map[string]interface{}, len(props))
				for propName, propSchema := range props {
					converted[propName] = toGeminiSchemaValue(propSchema)
				}
				out[key] = converted
			}
		case "items":
			if _, ok := value.(map[string]interface{}); ok {
				out[key] = toGeminiSchemaValue(value)
			}
		case "anyOf":
			if values, ok := value.([]interface{}); ok {
				converted := make([]interface{}, 0, len(values))
				for _, item := range values {
					converted = append(converted, toGeminiSchemaValue(item))
				}
				out[key] = converted
			}
		}
	}
	if _, hasType := out["type"]; !hasType {
		out["type"] = "object"
	}
	if out["type"] == "object" {
		if _, hasProps := out["properties"]; !hasProps {
			out["properties"] = map[string]interface{}{}
		}
	}
	return out
}

func toGeminiSchemaValue(schema interface{}) interface{} {
	schemaMap, ok := schema.(map[string]interface{})
	if !ok {
		return schema
	}
	out := make(map[string]interface{}, len(schemaMap))
	for key, value := range schemaMap {
		switch key {
		case "type", "format", "title", "description", "nullable", "enum",
			"maxItems", "minItems", "required", "minProperties", "maxProperties",
			"minLength", "maxLength", "pattern", "example", "propertyOrdering",
			"default", "minimum", "maximum":
			out[key] = value
		case "properties":
			if props, ok := value.(map[string]interface{}); ok {
				converted := make(map[string]interface{}, len(props))
				for propName, propSchema := range props {
					converted[propName] = toGeminiSchemaValue(propSchema)
				}
				out[key] = converted
			}
		case "items":
			if _, ok := value.(map[string]interface{}); ok {
				out[key] = toGeminiSchemaValue(value)
			}
		case "anyOf":
			if values, ok := value.([]interface{}); ok {
				converted := make([]interface{}, 0, len(values))
				for _, item := range values {
					converted = append(converted, toGeminiSchemaValue(item))
				}
				out[key] = converted
			}
		}
	}
	return out
}
