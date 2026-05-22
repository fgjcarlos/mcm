package schema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxValidationErrors = 5

// ValidationResult is the bounded operator-facing schema validation result.
type ValidationResult struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors,omitempty"`
}

// TopicFilterMatches returns whether an MQTT topic matches a filter using + and # wildcards.
func TopicFilterMatches(filter string, topic string) bool {
	filterLevels := strings.Split(filter, "/")
	topicLevels := strings.Split(topic, "/")
	for i, level := range filterLevels {
		if level == "#" {
			return i == len(filterLevels)-1
		}
		if i >= len(topicLevels) {
			return false
		}
		if level == "+" {
			continue
		}
		if level != topicLevels[i] {
			return false
		}
	}
	return len(filterLevels) == len(topicLevels)
}

// ValidateSchemaDocument validates the constrained JSON Schema subset supported by MCM.
func ValidateSchemaDocument(schemaDoc []byte) error {
	var doc map[string]any
	if err := json.Unmarshal(schemaDoc, &doc); err != nil {
		return fmt.Errorf("schema must be valid JSON: %w", err)
	}
	return validateSchemaNode("$", doc)
}

// ValidateJSONPayload validates a JSON payload against MCM's constrained JSON Schema subset.
func ValidateJSONPayload(schemaDoc []byte, payload []byte) (ValidationResult, error) {
	var doc map[string]any
	if err := json.Unmarshal(schemaDoc, &doc); err != nil {
		return ValidationResult{}, fmt.Errorf("schema must be valid JSON: %w", err)
	}
	if err := validateSchemaNode("$", doc); err != nil {
		return ValidationResult{}, err
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return ValidationResult{Valid: false, Errors: []string{"payload is not valid JSON"}}, nil
	}
	var errs []string
	validateValue("$", doc, value, &errs)
	if len(errs) > maxValidationErrors {
		errs = append(errs[:maxValidationErrors], "additional validation errors omitted")
	}
	return ValidationResult{Valid: len(errs) == 0, Errors: errs}, nil
}

func validateSchemaNode(path string, node map[string]any) error {
	if rawType, ok := node["type"]; ok {
		typeName, ok := rawType.(string)
		if !ok {
			return fmt.Errorf("%s.type must be a string", path)
		}
		if !supportedType(typeName) {
			return fmt.Errorf("%s.type %q is not supported", path, typeName)
		}
	}
	if rawRequired, ok := node["required"]; ok {
		required, ok := rawRequired.([]any)
		if !ok {
			return fmt.Errorf("%s.required must be an array", path)
		}
		for idx, item := range required {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("%s.required[%d] must be a string", path, idx)
			}
		}
	}
	if rawProps, ok := node["properties"]; ok {
		props, ok := rawProps.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.properties must be an object", path)
		}
		for name, rawChild := range props {
			child, ok := rawChild.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.properties.%s must be an object", path, name)
			}
			if err := validateSchemaNode(path+".properties."+name, child); err != nil {
				return err
			}
		}
	}
	if rawAdditional, ok := node["additionalProperties"]; ok {
		if _, ok := rawAdditional.(bool); !ok {
			return fmt.Errorf("%s.additionalProperties must be a boolean", path)
		}
	}
	if rawEnum, ok := node["enum"]; ok {
		values, ok := rawEnum.([]any)
		if !ok {
			return fmt.Errorf("%s.enum must be an array", path)
		}
		if len(values) == 0 {
			return fmt.Errorf("%s.enum must have at least one value", path)
		}
	}
	for _, key := range []string{"minimum", "maximum"} {
		if raw, ok := node[key]; ok {
			if _, ok := raw.(float64); !ok {
				return fmt.Errorf("%s.%s must be a number", path, key)
			}
		}
	}
	for _, key := range []string{"minLength", "maxLength"} {
		if raw, ok := node[key]; ok {
			f, ok := raw.(float64)
			if !ok || f < 0 || f != float64(int64(f)) {
				return fmt.Errorf("%s.%s must be a non-negative integer", path, key)
			}
		}
	}
	if rawPattern, ok := node["pattern"]; ok {
		pattern, ok := rawPattern.(string)
		if !ok {
			return fmt.Errorf("%s.pattern must be a string", path)
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("%s.pattern is not a valid regular expression: %w", path, err)
		}
	}
	if rawItems, ok := node["items"]; ok {
		items, ok := rawItems.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.items must be an object", path)
		}
		if err := validateSchemaNode(path+".items", items); err != nil {
			return err
		}
	}
	return nil
}

func supportedType(typeName string) bool {
	switch typeName {
	case "object", "array", "string", "number", "integer", "boolean", "null":
		return true
	default:
		return false
	}
}

func validateValue(path string, schema map[string]any, value any, errs *[]string) {
	if len(*errs) > maxValidationErrors {
		return
	}
	if rawType, ok := schema["type"].(string); ok && !valueMatchesType(value, rawType) {
		*errs = append(*errs, fmt.Sprintf("%s must be %s", path, rawType))
		return
	}
	if enumValues, ok := schema["enum"].([]any); ok {
		if !enumContains(enumValues, value) {
			*errs = append(*errs, fmt.Sprintf("%s must match one of the enum values", path))
		}
	}
	if numeric, ok := value.(float64); ok {
		if minimum, ok := schema["minimum"].(float64); ok && numeric < minimum {
			*errs = append(*errs, fmt.Sprintf("%s must be >= %v", path, minimum))
		}
		if maximum, ok := schema["maximum"].(float64); ok && numeric > maximum {
			*errs = append(*errs, fmt.Sprintf("%s must be <= %v", path, maximum))
		}
	}
	if str, ok := value.(string); ok {
		runeCount := utf8.RuneCountInString(str)
		if minLength, ok := schema["minLength"].(float64); ok && runeCount < int(minLength) {
			*errs = append(*errs, fmt.Sprintf("%s must have at least %d characters", path, int(minLength)))
		}
		if maxLength, ok := schema["maxLength"].(float64); ok && runeCount > int(maxLength) {
			*errs = append(*errs, fmt.Sprintf("%s must have at most %d characters", path, int(maxLength)))
		}
		if pattern, ok := schema["pattern"].(string); ok {
			// Pattern was validated at schema-doc time, so compile cannot fail here.
			if re, err := regexp.Compile(pattern); err == nil && !re.MatchString(str) {
				*errs = append(*errs, fmt.Sprintf("%s must match pattern %q", path, pattern))
			}
		}
	}
	if arr, isArray := value.([]any); isArray {
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for idx, element := range arr {
				if len(*errs) > maxValidationErrors {
					return
				}
				validateValue(fmt.Sprintf("%s[%d]", path, idx), itemSchema, element, errs)
			}
		}
	}
	object, isObject := value.(map[string]any)
	if !isObject {
		return
	}
	if rawRequired, ok := schema["required"].([]any); ok {
		for _, item := range rawRequired {
			name, _ := item.(string)
			if _, exists := object[name]; !exists {
				*errs = append(*errs, fmt.Sprintf("%s.%s is required", path, name))
			}
		}
	}
	props, _ := schema["properties"].(map[string]any)
	for name, rawChild := range props {
		child, _ := rawChild.(map[string]any)
		if child == nil {
			continue
		}
		if childValue, exists := object[name]; exists {
			validateValue(path+"."+name, child, childValue, errs)
		}
	}
	if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
		for name := range object {
			if _, known := props[name]; !known {
				*errs = append(*errs, fmt.Sprintf("%s.%s is not allowed", path, name))
			}
		}
	}
}

func enumContains(values []any, candidate any) bool {
	for _, v := range values {
		if reflect.DeepEqual(v, candidate) {
			return true
		}
	}
	return false
}

func valueMatchesType(value any, typeName string) bool {
	switch typeName {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		f, ok := value.(float64)
		return ok && f == float64(int64(f))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}
