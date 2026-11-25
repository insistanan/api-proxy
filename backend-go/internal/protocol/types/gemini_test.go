package types

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestGeminiPart_TextAndThoughtSignaturesSurviveReplay(t *testing.T) {
	for _, input := range []string{
		`{"text":"thought","thought":true,"thoughtSignature":"original-signature"}`,
		`{"text":"answer","thoughtSignature":"original-signature"}`,
	} {
		var part GeminiPart
		if err := json.Unmarshal([]byte(input), &part); err != nil {
			t.Fatal(err)
		}
		output, err := json.Marshal(part)
		if err != nil {
			t.Fatal(err)
		}
		if string(output) != input {
			t.Fatalf("原生回放不应丢失思考/文本 part 的签名: got %s want %s", output, input)
		}
	}
}

func TestGeminiFunctionDeclaration_PreservesSchemaDialect(t *testing.T) {
	input := `{"name":"run","parametersJsonSchema":{"type":"object","properties":{"title":{"type":"string"}},"additionalProperties":false,"const":{"mode":"fast"}}}`
	var declaration GeminiFunctionDeclaration
	if err := json.Unmarshal([]byte(input), &declaration); err != nil {
		t.Fatal(err)
	}
	if declaration.Parameters != nil || declaration.ParametersJSONSchema == nil {
		t.Fatal("schema 方言没有保留")
	}
	encoded, err := json.Marshal(declaration)
	if err != nil {
		t.Fatal(err)
	}
	var before, after interface{}
	if err := json.Unmarshal([]byte(input), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("schema 被修改: %s", encoded)
	}
}
