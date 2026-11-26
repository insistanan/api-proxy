package clients

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// tomlCodeLines 标记处于字符串外的行首，防止提示词中的配置示例被当作真实表头。
func tomlCodeLines(lines []string) []bool {
	code := make([]bool, len(lines))
	var multiline byte
	depth := 0
	for index, line := range lines {
		code[index] = multiline == 0 && depth == 0
		var quote byte
		for i := 0; i < len(line); i++ {
			ch := line[i]
			if multiline != 0 {
				if multiline == '"' && ch == '\\' {
					i++
					continue
				}
				if ch == multiline && i+2 < len(line) && line[i+1] == ch && line[i+2] == ch {
					for i+1 < len(line) && line[i+1] == ch {
						i++
					}
					multiline = 0
				}
				continue
			}
			if quote != 0 {
				if quote == '"' && ch == '\\' {
					i++
					continue
				}
				if ch == quote {
					quote = 0
				}
				continue
			}
			if ch == '#' {
				break
			}
			if ch == '"' || ch == '\'' {
				if i+2 < len(line) && line[i+1] == ch && line[i+2] == ch {
					multiline = ch
					i += 2
				} else {
					quote = ch
				}
				continue
			}
			if ch == '[' || ch == '{' {
				depth++
			}
			if ch == ']' || ch == '}' {
				depth--
			}
		}
	}
	return code
}

// verifyCodexEdit 比较完整语义树，保证文本补丁只改变指定字段。
func verifyCodexEdit(previous, next []byte, path []string, value *string) error {
	var expected, actual map[string]interface{}
	if err := toml.Unmarshal(previous, &expected); err != nil {
		return err
	}
	if expected == nil {
		expected = make(map[string]interface{})
	}
	if err := toml.Unmarshal(next, &actual); err != nil {
		return err
	}
	if actual == nil {
		actual = make(map[string]interface{})
	}
	object := expected
	for _, key := range path[:len(path)-1] {
		child, exists := object[key]
		if !exists {
			child = make(map[string]interface{})
			object[key] = child
		}
		var ok bool
		object, ok = child.(map[string]interface{})
		if !ok {
			return fmt.Errorf("TOML 字段 %s 的父节点不是表", strings.Join(path, "."))
		}
	}
	key := path[len(path)-1]
	if value == nil {
		delete(object, key)
	} else {
		object[key] = *value
	}
	if !reflect.DeepEqual(expected, actual) {
		return fmt.Errorf("无法无损修改 TOML 字段 %s，请检查该字段的表达形式", strings.Join(path, "."))
	}
	return nil
}
