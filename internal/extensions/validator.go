package extensions

import (
	"fmt"
	"math"
	"regexp"
)

// applyDefaults fills in missing settings keys from their SchemaNode defaults.
// Returns the (possibly updated) settings map and any warnings encountered.
// The input map is not modified in place; a new map is returned.
func applyDefaults(settings map[string]any, schema map[string]SchemaNode) (map[string]any, []string) {
	if len(schema) == 0 {
		return settings, nil
	}

	out := make(map[string]any, len(settings))
	for k, v := range settings {
		out[k] = v
	}

	var warns []string
	for key, node := range schema {
		// Warn about unsupported keywords captured in node.Unknown.
		for k := range node.Unknown {
			warns = append(warns, fmt.Sprintf("schema key %q: unsupported keyword %q (ignored)", key, k))
		}

		if _, exists := out[key]; !exists && node.Default != nil {
			out[key] = node.Default
		}
	}
	return out, warns
}

// validateLenient checks value against schema at the given path, returning
// human-readable warning strings for every violation found.
// It never returns an error; all problems are warnings.
// Used during extension load to catch misconfigured settings without blocking startup.
func validateLenient(value any, schema SchemaNode, path string) []string {
	var warns []string

	// Warn about unsupported schema keywords.
	for k := range schema.Unknown {
		warns = append(warns, fmt.Sprintf("schema %s: unsupported keyword %q (ignored)", path, k))
	}

	if value == nil {
		return warns
	}

	// Type check.
	if schema.Type != "" && !matchesType(value, schema.Type) {
		warns = append(warns, fmt.Sprintf("setting %s: expected type %q, got %T", path, schema.Type, value))
		// Further checks are meaningless after a type mismatch.
		return warns
	}

	// Enum membership.
	if len(schema.Enum) > 0 && !inEnum(value, schema.Enum) {
		warns = append(warns, fmt.Sprintf("setting %s: value not in allowed enum values %v", path, schema.Enum))
	}

	// Numeric bounds.
	if schema.Type == "number" || schema.Type == "integer" {
		n := toFloat64(value)
		if schema.Minimum != nil && n < *schema.Minimum {
			warns = append(warns, fmt.Sprintf("setting %s: value %v is below minimum %v", path, value, *schema.Minimum))
		}
		if schema.Maximum != nil && n > *schema.Maximum {
			warns = append(warns, fmt.Sprintf("setting %s: value %v is above maximum %v", path, value, *schema.Maximum))
		}
	}

	// String constraints.
	if schema.Type == "string" {
		s, _ := value.(string)
		if schema.MinLength != nil && len(s) < *schema.MinLength {
			warns = append(warns, fmt.Sprintf("setting %s: string length %d is below minLength %d", path, len(s), *schema.MinLength))
		}
		if schema.MaxLength != nil && len(s) > *schema.MaxLength {
			warns = append(warns, fmt.Sprintf("setting %s: string length %d is above maxLength %d", path, len(s), *schema.MaxLength))
		}
		if schema.Pattern != "" {
			re, err := regexp.Compile(schema.Pattern)
			if err != nil {
				warns = append(warns, fmt.Sprintf("schema %s: invalid pattern %q: %v", path, schema.Pattern, err))
			} else if !re.MatchString(s) {
				warns = append(warns, fmt.Sprintf("setting %s: value does not match pattern %q", path, schema.Pattern))
			}
		}
	}

	// Array items.
	if schema.Type == "array" && schema.Items != nil {
		if arr, ok := value.([]any); ok {
			for i, item := range arr {
				warns = append(warns, validateLenient(item, *schema.Items, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	}

	// Object properties.
	if schema.Type == "object" {
		obj, ok := value.(map[string]any)
		if ok {
			for _, req := range schema.Required {
				if _, exists := obj[req]; !exists {
					warns = append(warns, fmt.Sprintf("setting %s.%s: required property missing", path, req))
				}
			}
			for k, propSchema := range schema.Properties {
				if v, exists := obj[k]; exists {
					warns = append(warns, validateLenient(v, propSchema, path+"."+k)...)
				}
			}
		}
	}

	return warns
}

// validateStrict checks value against schema, returning an error on the first
// violation. Used by SettingsSet to enforce type safety on writes.
func validateStrict(value any, schema SchemaNode, path string) error {
	if schema.Type != "" && !matchesType(value, schema.Type) {
		return fmt.Errorf("type mismatch at %s: expected %q, got %T", path, schema.Type, value)
	}
	if len(schema.Enum) > 0 && !inEnum(value, schema.Enum) {
		return fmt.Errorf("value at %s is not in allowed enum values %v", path, schema.Enum)
	}
	if value == nil {
		return nil
	}
	if schema.Type == "number" || schema.Type == "integer" {
		n := toFloat64(value)
		if schema.Minimum != nil && n < *schema.Minimum {
			return fmt.Errorf("value at %s (%v) is below minimum %v", path, value, *schema.Minimum)
		}
		if schema.Maximum != nil && n > *schema.Maximum {
			return fmt.Errorf("value at %s (%v) is above maximum %v", path, value, *schema.Maximum)
		}
	}
	if schema.Type == "string" {
		s, _ := value.(string)
		if schema.MinLength != nil && len(s) < *schema.MinLength {
			return fmt.Errorf("value at %s: string length %d is below minLength %d", path, len(s), *schema.MinLength)
		}
		if schema.MaxLength != nil && len(s) > *schema.MaxLength {
			return fmt.Errorf("value at %s: string length %d is above maxLength %d", path, len(s), *schema.MaxLength)
		}
		if schema.Pattern != "" {
			re, err := regexp.Compile(schema.Pattern)
			if err != nil {
				return fmt.Errorf("schema %s: invalid pattern %q: %w", path, schema.Pattern, err)
			}
			if !re.MatchString(s) {
				return fmt.Errorf("value at %s does not match pattern %q", path, schema.Pattern)
			}
		}
	}
	return nil
}

// matchesType returns true if value is compatible with the given JSON Schema type string.
// Handles both Go native types (from settings_set callers) and JSON-decoded types
// (float64 for all numbers, []any for arrays, map[string]any for objects).
func matchesType(value any, schemaType string) bool {
	if value == nil {
		return schemaType == "null"
	}
	switch schemaType {
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		switch value.(type) {
		case float32, float64,
			int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64:
			return true
		}
		return false
	case "integer":
		switch v := value.(type) {
		case int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64:
			_ = v
			return true
		case float64:
			return v == math.Trunc(v) && !math.IsInf(v, 0)
		case float32:
			return float64(v) == math.Trunc(float64(v))
		}
		return false
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		switch value.(type) {
		case []any, []string, []int, []int64, []float64:
			return true
		}
		return false
	case "object":
		switch value.(type) {
		case map[string]any:
			return true
		}
		return false
	case "null":
		return value == nil
	}
	// Unknown type — don't reject.
	return true
}

// inEnum reports whether value equals any element of enum.
// Comparison is by fmt.Sprintf representation for cross-type safety.
func inEnum(value any, enum []any) bool {
	vs := fmt.Sprintf("%v", value)
	for _, e := range enum {
		if fmt.Sprintf("%v", e) == vs {
			return true
		}
	}
	return false
}

// toFloat64 converts numeric Go types to float64 for bound checking.
func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int8:
		return float64(n)
	case int16:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case uint:
		return float64(n)
	case uint8:
		return float64(n)
	case uint16:
		return float64(n)
	case uint32:
		return float64(n)
	case uint64:
		return float64(n)
	}
	return 0
}
