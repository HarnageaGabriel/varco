package schematest

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
)

// schema is a minimal JSON Schema validator. It implements the draft 2020-12
// keywords the published varco schemas use and nothing else: type, const,
// enum, required, properties, additionalProperties, items and a local $ref
// into #/$defs. A document it accepts is therefore also accepted by a
// standard validator, and a document it rejects is rejected for the reason it
// names.
type schema struct {
	root map[string]any
	defs map[string]any
}

// loadSchema reads a schema file and indexes its $defs.
func loadSchema(path string) (*schema, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	defs := make(map[string]any)
	if raw, ok := root["$defs"].(map[string]any); ok {
		for name, value := range raw {
			defs[name] = value
		}
	}
	return &schema{root: root, defs: defs}, nil
}

// validate checks a decoded document against the schema and returns every
// violation it found. An empty result means the document conforms.
func (s *schema) validate(document any) []error {
	return s.check(document, s.root, "$")
}

// check applies one subschema to one value and returns the violations.
func (s *schema) check(value any, spec map[string]any, path string) []error {
	problems := make([]error, 0, 4)
	if raw, ok := spec["$ref"]; ok {
		name, ok := raw.(string)
		if !ok {
			return append(problems, errors.New(path+": $ref must be a string"))
		}
		target, ok := s.resolve(name)
		if !ok {
			return append(problems, errors.New(path+": unknown $ref "+name))
		}
		return append(problems, s.check(value, target, path)...)
	}
	if raw, ok := spec["type"]; ok {
		if !matchesType(value, raw) {
			return append(problems, fmt.Errorf("%s: expected type %s, got %s", path, typeNames(raw), kindName(value)))
		}
	}
	if raw, ok := spec["const"]; ok {
		if !equalValue(value, raw) {
			return append(problems, fmt.Errorf("%s: expected the constant %v", path, raw))
		}
	}
	if raw, ok := spec["enum"]; ok {
		allowed, ok := raw.([]any)
		if !ok {
			return append(problems, errors.New(path+": enum must be an array"))
		}
		matched := false
		for _, candidate := range allowed {
			if equalValue(value, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return append(problems, fmt.Errorf("%s: %v is not one of %v", path, value, allowed))
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		problems = append(problems, s.checkObject(typed, spec, path)...)
	case []any:
		problems = append(problems, s.checkArray(typed, spec, path)...)
	}
	return problems
}

// checkObject applies the object keywords: required, properties and
// additionalProperties.
func (s *schema) checkObject(object map[string]any, spec map[string]any, path string) []error {
	problems := make([]error, 0, 4)
	if raw, ok := spec["required"]; ok {
		names, ok := raw.([]any)
		if !ok {
			return append(problems, errors.New(path+": required must be an array"))
		}
		for _, entry := range names {
			name, ok := entry.(string)
			if !ok {
				continue
			}
			if _, present := object[name]; !present {
				problems = append(problems, fmt.Errorf("%s.%s: required property is missing", path, name))
			}
		}
	}
	properties, _ := spec["properties"].(map[string]any)
	for name, raw := range properties {
		subschema, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		value, present := object[name]
		if !present {
			continue
		}
		problems = append(problems, s.check(value, subschema, path+"."+name)...)
	}
	allowed, ok := spec["additionalProperties"].(bool)
	if ok && !allowed {
		for name := range object {
			if _, known := properties[name]; known {
				continue
			}
			problems = append(problems, fmt.Errorf("%s.%s: unknown property is not allowed", path, name))
		}
	}
	return problems
}

// checkArray applies the items keyword to every element of an array.
func (s *schema) checkArray(items []any, spec map[string]any, path string) []error {
	raw, ok := spec["items"]
	if !ok {
		return nil
	}
	subschema, ok := raw.(map[string]any)
	if !ok {
		return []error{errors.New(path + ": items must be a schema object")}
	}
	problems := make([]error, 0, len(items))
	for index, item := range items {
		problems = append(problems, s.check(item, subschema, path+"["+strconv.Itoa(index)+"]")...)
	}
	return problems
}

// resolve returns the $defs entry a $ref points at. Only a local reference
// into #/$defs is supported.
func (s *schema) resolve(ref string) (map[string]any, bool) {
	const prefix = "#/$defs/"
	if !strings.HasPrefix(ref, prefix) {
		return nil, false
	}
	target, ok := s.defs[strings.TrimPrefix(ref, prefix)]
	if !ok {
		return nil, false
	}
	subschema, ok := target.(map[string]any)
	return subschema, ok
}

// matchesType reports whether a value matches the type of a schema, which is
// either one name or a list of names.
func matchesType(value any, spec any) bool {
	switch typed := spec.(type) {
	case string:
		return matchesTypeName(value, typed)
	case []any:
		for _, entry := range typed {
			name, ok := entry.(string)
			if ok && matchesTypeName(value, name) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// matchesTypeName reports whether a value matches one JSON type name.
func matchesTypeName(value any, name string) bool {
	switch name {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	case "number":
		_, ok := toNumber(value)
		return ok
	case "integer":
		number, ok := toNumber(value)
		return ok && number == math.Trunc(number)
	default:
		return false
	}
}

// typeNames renders the expected type of a schema for an error message.
func typeNames(spec any) string {
	switch typed := spec.(type) {
	case string:
		return typed
	case []any:
		names := make([]string, 0, len(typed))
		for _, entry := range typed {
			name, ok := entry.(string)
			if !ok {
				continue
			}
			names = append(names, name)
		}
		return strings.Join(names, " or ")
	default:
		return "unknown"
	}
}

// kindName names the JSON type of a value for an error message.
func kindName(value any) string {
	switch value.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		if _, ok := toNumber(value); ok {
			return "number"
		}
		return "unknown"
	}
}

// equalValue reports whether two decoded JSON values are equal. Numbers
// compare by value, so a number built in Go equals the float64 a decoder
// produces for the same number.
func equalValue(left, right any) bool {
	leftNumber, leftIsNumber := toNumber(left)
	rightNumber, rightIsNumber := toNumber(right)
	if leftIsNumber || rightIsNumber {
		return leftIsNumber && rightIsNumber && leftNumber == rightNumber
	}
	return reflect.DeepEqual(left, right)
}

// toNumber returns the numeric value of a JSON number.
func toNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	default:
		return 0, false
	}
}
