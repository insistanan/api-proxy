package types

import (
	"encoding/json"
	"testing"
)

func TestGeminiPart_UnmarshalJSON_ThoughtSignatureAtPartLevel(t *testing.T) {
	t.Run("驼峰 thoughtSignature 在 part 层级时归一化到 functionCall，并在 part 层级输出 thoughtSignature", func(t *testing.T) {
		input := `{"functionCall":{"name":"list_directory","args":{"path":"."}},"thoughtSignature":"sig_camel"}`

		var part GeminiPart
		if err := json.Unmarshal([]byte(input), &part); err != nil {
			t.Fatalf("UnmarshalJSON 失败: %v", err)
		}
		if part.FunctionCall == nil {
			t.Fatalf("FunctionCall 为空")
		}
		if part.FunctionCall.ThoughtSignature != "sig_camel" {
			t.Fatalf("ThoughtSignature=%q, want=%q", part.FunctionCall.ThoughtSignature, "sig_camel")
		}

		outBytes, err := json.Marshal(part)
		if err != nil {
			t.Fatalf("Marshal 失败: %v", err)
		}

		var got map[string]interface{}
		if err := json.Unmarshal(outBytes, &got); err != nil {
			t.Fatalf("解析输出 JSON 失败: %v", err)
		}
		if v, ok := got["thoughtSignature"]; !ok || v != "sig_camel" {
			t.Fatalf("part.thoughtSignature=%v, want=%v", v, "sig_camel")
		}
		fc, ok := got["functionCall"].(map[string]interface{})
		if !ok {
			t.Fatalf("functionCall 类型=%T, want=map[string]interface{}", got["functionCall"])
		}
		if _, ok := fc["thoughtSignature"]; ok {
			t.Fatalf("不应在 functionCall 内输出 thoughtSignature: %v", fc)
		}
		if _, ok := fc["thought_signature"]; ok {
			t.Fatalf("不应在 functionCall 内输出 thought_signature: %v", fc)
		}
	})

	t.Run("下划线 thought_signature 在 part 层级时归一化到 functionCall，并在 part 层级输出 thoughtSignature", func(t *testing.T) {
		input := `{"functionCall":{"name":"list_directory","args":{"path":"."}},"thought_signature":"sig_snake"}`

		var part GeminiPart
		if err := json.Unmarshal([]byte(input), &part); err != nil {
			t.Fatalf("UnmarshalJSON 失败: %v", err)
		}
		if part.FunctionCall == nil {
			t.Fatalf("FunctionCall 为空")
		}
		if part.FunctionCall.ThoughtSignature != "sig_snake" {
			t.Fatalf("ThoughtSignature=%q, want=%q", part.FunctionCall.ThoughtSignature, "sig_snake")
		}

		outBytes, err := json.Marshal(part)
		if err != nil {
			t.Fatalf("Marshal 失败: %v", err)
		}

		var got map[string]interface{}
		if err := json.Unmarshal(outBytes, &got); err != nil {
			t.Fatalf("解析输出 JSON 失败: %v", err)
		}
		if v, ok := got["thoughtSignature"]; !ok || v != "sig_snake" {
			t.Fatalf("part.thoughtSignature=%v, want=%v", v, "sig_snake")
		}
		if _, ok := got["thought_signature"]; ok {
			t.Fatalf("不应在 part 层级输出 thought_signature: %v", got)
		}

		fc, ok := got["functionCall"].(map[string]interface{})
		if !ok {
			t.Fatalf("functionCall 类型=%T, want=map[string]interface{}", got["functionCall"])
		}
		if _, ok := fc["thoughtSignature"]; ok {
			t.Fatalf("不应在 functionCall 内输出 thoughtSignature: %v", fc)
		}
		if _, ok := fc["thought_signature"]; ok {
			t.Fatalf("不应在 functionCall 内输出 thought_signature: %v", fc)
		}
	})
}

func TestGeminiFunctionDeclaration_UnmarshalJSON_ParametersJsonSchema(t *testing.T) {
	input := `{
	  "name": "list_directory",
	  "description": "Lists the names of files and subdirectories directly within a specified directory path.",
	  "parametersJsonSchema": {
	    "type": "object",
	    "properties": {
	      "dir_path": { "type": "string" }
	    },
	    "required": ["dir_path"]
	  }
	}`

	var decl GeminiFunctionDeclaration
	if err := json.Unmarshal([]byte(input), &decl); err != nil {
		t.Fatalf("UnmarshalJSON 失败: %v", err)
	}
	if decl.Name != "list_directory" {
		t.Fatalf("Name=%q, want=%q", decl.Name, "list_directory")
	}
	if decl.Parameters == nil {
		t.Fatalf("Parameters 为空，期望从 parametersJsonSchema 读取")
	}

	outBytes, err := json.Marshal(decl)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(outBytes, &got); err != nil {
		t.Fatalf("解析输出 JSON 失败: %v", err)
	}
	if _, ok := got["parameters"]; !ok {
		t.Fatalf("输出缺少 parameters 字段: %v", got)
	}
	if _, ok := got["parametersJsonSchema"]; ok {
		t.Fatalf("不应输出 parametersJsonSchema 字段: %v", got)
	}
}

func TestGeminiFunctionDeclaration_UnmarshalJSON_SanitizeParametersSchema(t *testing.T) {
	input := `{
  "name": "delegate_to_agent",
  "description": "Delegates a task to a specialized sub-agent.",
  "parametersJsonSchema": {
    "$schema": "http://json-schema.org/draft-07/schema#",
    "type": "object",
    "additionalProperties": false,
    "properties": {
      "agent_name": {
        "type": "string",
        "const": "codebase_investigator",
        "additionalProperties": false
      }
    },
    "required": ["agent_name"]
  }
}`

	var decl GeminiFunctionDeclaration
	if err := json.Unmarshal([]byte(input), &decl); err != nil {
		t.Fatalf("UnmarshalJSON 失败: %v", err)
	}

	params, ok := decl.Parameters.(map[string]interface{})
	if !ok {
		t.Fatalf("Parameters 类型=%T, want=map[string]interface{}", decl.Parameters)
	}
	if _, ok := params["$schema"]; ok {
		t.Fatalf("不应包含 $schema: %v", params)
	}
	if _, ok := params["additionalProperties"]; ok {
		t.Fatalf("不应包含 additionalProperties: %v", params)
	}

	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("properties 类型=%T, want=map[string]interface{}", params["properties"])
	}
	agentName, ok := props["agent_name"].(map[string]interface{})
	if !ok {
		t.Fatalf("properties.agent_name 类型=%T, want=map[string]interface{}", props["agent_name"])
	}
	if _, ok := agentName["const"]; ok {
		t.Fatalf("不应包含 const: %v", agentName)
	}
	if _, ok := agentName["additionalProperties"]; ok {
		t.Fatalf("不应包含 additionalProperties(嵌套): %v", agentName)
	}
	enum, ok := agentName["enum"].([]interface{})
	if !ok || len(enum) != 1 || enum[0] != "codebase_investigator" {
		t.Fatalf("agent_name.enum=%v, want=%v", agentName["enum"], []interface{}{"codebase_investigator"})
	}

	outBytes, err := json.Marshal(decl)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(outBytes, &got); err != nil {
		t.Fatalf("解析输出 JSON 失败: %v", err)
	}
	outParams, ok := got["parameters"].(map[string]interface{})
	if !ok {
		t.Fatalf("输出 parameters 类型=%T, want=map[string]interface{}", got["parameters"])
	}
	if _, ok := outParams["$schema"]; ok {
		t.Fatalf("输出不应包含 $schema: %v", outParams)
	}
	if _, ok := outParams["additionalProperties"]; ok {
		t.Fatalf("输出不应包含 additionalProperties: %v", outParams)
	}
	outProps, ok := outParams["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("输出 properties 类型=%T, want=map[string]interface{}", outParams["properties"])
	}
	outAgentName, ok := outProps["agent_name"].(map[string]interface{})
	if !ok {
		t.Fatalf("输出 properties.agent_name 类型=%T, want=map[string]interface{}", outProps["agent_name"])
	}
	if _, ok := outAgentName["const"]; ok {
		t.Fatalf("输出不应包含 const: %v", outAgentName)
	}
}

func TestSanitizeGeminiToolSchema_RemovesGeminiUnsupportedJSONSchemaKeywords(t *testing.T) {
	input := map[string]interface{}{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":          "string",
				"propertyNames": map[string]interface{}{"pattern": "^[a-z]+$"},
			},
			"count": map[string]interface{}{
				"type":             "integer",
				"exclusiveMinimum": float64(0),
			},
			"mode": map[string]interface{}{
				"type":  "string",
				"const": "fast",
			},
			"nullable_text": map[string]interface{}{
				"type": []interface{}{"null", "string"},
			},
		},
		"required": []interface{}{"query"},
	}

	got, ok := SanitizeGeminiToolSchema(input).(map[string]interface{})
	if !ok {
		t.Fatalf("清理结果类型=%T, want=map[string]interface{}", got)
	}
	if _, ok := got["$schema"]; ok {
		t.Fatalf("不应包含 $schema: %v", got)
	}

	props, ok := got["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("properties 类型=%T, want=map[string]interface{}", got["properties"])
	}
	if _, ok := props["query"]; !ok {
		t.Fatalf("不应删除 properties 下的参数名: %v", props)
	}

	query := props["query"].(map[string]interface{})
	if _, ok := query["propertyNames"]; ok {
		t.Fatalf("不应包含 propertyNames: %v", query)
	}

	count := props["count"].(map[string]interface{})
	if _, ok := count["exclusiveMinimum"]; ok {
		t.Fatalf("不应包含 exclusiveMinimum: %v", count)
	}

	mode := props["mode"].(map[string]interface{})
	if _, ok := mode["const"]; ok {
		t.Fatalf("不应包含 const: %v", mode)
	}
	enum, ok := mode["enum"].([]interface{})
	if !ok || len(enum) != 1 || enum[0] != "fast" {
		t.Fatalf("mode.enum=%v, want=%v", mode["enum"], []interface{}{"fast"})
	}

	nullableText := props["nullable_text"].(map[string]interface{})
	if nullableText["type"] != "string" {
		t.Fatalf("nullable_text.type=%v, want=string", nullableText["type"])
	}
	if nullableText["nullable"] != true {
		t.Fatalf("nullable_text.nullable=%v, want=true", nullableText["nullable"])
	}
}
