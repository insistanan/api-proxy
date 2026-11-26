package clients

import (
	"fmt"
	"math"
)

func configObjectField(root map[string]interface{}, key, path string) (map[string]interface{}, error) {
	value, exists := root[key]
	if !exists {
		return map[string]interface{}{}, nil
	}
	object, ok := value.(map[string]interface{})
	if !ok || object == nil {
		return nil, fmt.Errorf("%s 必须是对象", path)
	}
	return object, nil
}

func configStringFields(object map[string]interface{}, path string, fields ...string) error {
	for _, field := range fields {
		if value, exists := object[field]; exists {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s.%s 必须是字符串", path, field)
			}
		}
	}
	return nil
}

func validateOpenCodeShape(root map[string]interface{}) error {
	if root == nil {
		return fmt.Errorf("OpenCode 根节点必须是对象")
	}
	if err := configStringFields(root, "配置", "model", "small_model"); err != nil {
		return err
	}
	providers, err := configObjectField(root, "provider", "provider")
	if err != nil {
		return err
	}
	for name := range providers {
		path := "provider." + name
		provider, err := configObjectField(providers, name, path)
		if err != nil {
			return err
		}
		if err := configStringFields(provider, path, "name", "npm"); err != nil {
			return err
		}
		options, err := configObjectField(provider, "options", path+".options")
		if err != nil {
			return err
		}
		if err := configStringFields(options, path+".options", "baseURL", "apiKey"); err != nil {
			return err
		}
		headers, err := configObjectField(options, "headers", path+".options.headers")
		if err != nil {
			return err
		}
		for key := range headers {
			if err := configStringFields(headers, path+".options.headers", key); err != nil {
				return err
			}
		}
		models, err := configObjectField(provider, "models", path+".models")
		if err != nil {
			return err
		}
		for key := range models {
			modelPath := path + ".models." + key
			model, err := configObjectField(models, key, modelPath)
			if err != nil {
				return err
			}
			if err := configStringFields(model, modelPath, "id", "name"); err != nil {
				return err
			}
			if _, err := configObjectField(model, "options", modelPath+".options"); err != nil {
				return err
			}
			limits, err := configObjectField(model, "limit", modelPath+".limit")
			if err != nil {
				return err
			}
			for _, field := range []string{"context", "input", "output"} {
				if value, exists := limits[field]; exists {
					number, ok := value.(float64)
					if !ok || number < 0 || math.Trunc(number) != number {
						return fmt.Errorf("%s.limit.%s 必须是非负整数", modelPath, field)
					}
				}
			}
			variants, err := configObjectField(model, "variants", modelPath+".variants")
			if err != nil {
				return err
			}
			for variantName := range variants {
				variant, err := configObjectField(variants, variantName, modelPath+".variants."+variantName)
				if err != nil {
					return err
				}
				if err := configStringFields(variant, modelPath+".variants."+variantName, "reasoningEffort"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateDSHShape(root map[string]interface{}) error {
	piAI, err := configObjectField(root, "llm-pi-ai", "llm-pi-ai")
	if err != nil {
		return err
	}
	providers, err := configObjectField(piAI, "providers", "llm-pi-ai.providers")
	if err != nil {
		return err
	}
	for name := range providers {
		provider, err := configObjectField(providers, name, "providers."+name)
		if err != nil {
			return err
		}
		if err := configStringFields(provider, "providers."+name, "apiKeyEnv", "api", "baseURL", "displayName"); err != nil {
			return err
		}
		value, exists := provider["models"]
		if !exists {
			continue
		}
		models, ok := value.([]interface{})
		if !ok {
			return fmt.Errorf("providers.%s.models 必须是数组", name)
		}
		for index, value := range models {
			path := fmt.Sprintf("providers.%s.models[%d]", name, index)
			model, ok := value.(map[string]interface{})
			if !ok {
				return fmt.Errorf("%s 必须是对象", path)
			}
			if err := configStringFields(model, path, "id", "name"); err != nil {
				return err
			}
			for _, field := range []string{"contextWindow", "maxTokens"} {
				if value, exists := model[field]; exists {
					number, ok := value.(int)
					if !ok || number <= 0 {
						return fmt.Errorf("%s.%s 必须是正整数", path, field)
					}
				}
			}
			if value, exists := model["input"]; exists {
				items, ok := value.([]interface{})
				if !ok {
					return fmt.Errorf("%s.input 必须是字符串数组", path)
				}
				for _, item := range items {
					if _, ok := item.(string); !ok {
						return fmt.Errorf("%s.input 必须是字符串数组", path)
					}
				}
			}
		}
	}
	for key, fields := range map[string][]string{"agent-default-model": {"provider", "model"}, "ui-theme": {"preference"}} {
		object, err := configObjectField(root, key, key)
		if err != nil {
			return err
		}
		if err := configStringFields(object, key, fields...); err != nil {
			return err
		}
	}
	return nil
}
