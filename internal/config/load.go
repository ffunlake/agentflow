package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads one .yaml, .yml or .json document, checks its exact schema and
// validates values. Relative paths remain source values until Resolve.
func Load(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, &FieldError{Path: "$", Message: "resolve config path", Cause: err}
	}
	ext := strings.ToLower(filepath.Ext(abs))
	if ext != ".yaml" && ext != ".yml" && ext != ".json" {
		return nil, invalid("$", "config file must have .yaml, .yml or .json extension")
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, &FieldError{Path: "$", Message: "read config", Cause: err}
	}
	// YAML accepts a superset of JSON. Check JSON grammar first so a .json file
	// cannot silently accept YAML syntax, trailing values or trailing commas.
	if ext == ".json" && !json.Valid(data) {
		return nil, invalid("$", "invalid JSON document")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		return nil, &FieldError{Path: "$", Message: "decode config", Cause: err}
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, invalid("$", "expected exactly one document")
	}
	if len(doc.Content) != 1 {
		return nil, invalid("$", "expected an object")
	}
	// Check nodes before decoding: yaml's ordinary struct decoder coerces
	// numbers/bools into strings. This walk also catches duplicate keys before
	// any map can overwrite them. There is no untyped intermediate object.
	if err := checkSchema(doc.Content[0], reflect.TypeFor[Config](), "$"); err != nil {
		return nil, err
	}
	var c Config
	if err := doc.Decode(&c); err != nil {
		return nil, &FieldError{Path: "$", Message: "decode config", Cause: err}
	}
	c.SourcePath = abs
	if err := Validate(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

// checkSchema follows the Go schema's yaml tags, keeping field/type rules in one
// place. Aliases and merge keys are deliberately unsupported: they must not hide
// duplicate or unknown fields, nor introduce recursive source trees.
func checkSchema(node *yaml.Node, typ reflect.Type, path string) error {
	if node.Kind == yaml.AliasNode || node.Tag == "!!null" {
		return invalid(path, "nulls and YAML aliases are not supported")
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct, reflect.Map:
		if node.Kind != yaml.MappingNode || node.Tag != "!!map" {
			return invalid(path, "expected an object")
		}
		fields := make(map[string]reflect.Type)
		if typ.Kind() == reflect.Struct {
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				name := strings.Split(field.Tag.Get("yaml"), ",")[0]
				if name != "-" {
					fields[name] = field.Type
				}
			}
		}
		seen := make(map[string]bool)
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return invalid(path, "object keys must be strings; YAML merge keys are not supported")
			}
			fieldPath := key.Value
			if path != "$" {
				fieldPath = path + "." + key.Value
			}
			if seen[key.Value] {
				return invalid(fieldPath, "duplicate object key")
			}
			seen[key.Value] = true
			var fieldType reflect.Type
			if typ.Kind() == reflect.Map {
				fieldType = typ.Elem()
			} else {
				var ok bool
				fieldType, ok = fields[key.Value]
				if !ok {
					return invalid(fieldPath, "unknown field")
				}
			}
			if err := checkSchema(value, fieldType, fieldPath); err != nil {
				return err
			}
			// Empty layout discriminators would erase field presence in Layout's
			// string fields and could hide a leaf/split conflict during compilation.
			if typ == reflect.TypeFor[Layout]() && (key.Value == "agent" || key.Value == "split") && value.Value == "" {
				return invalid(fieldPath, "must be non-empty when present")
			}
		}
	case reflect.Slice:
		if node.Kind != yaml.SequenceNode || node.Tag != "!!seq" {
			return invalid(path, "expected an array")
		}
		for i, child := range node.Content {
			if err := checkSchema(child, typ.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.String:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
			return invalid(path, "expected a string")
		}
	case reflect.Int:
		var value int
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" || node.Decode(&value) != nil {
			return invalid(path, "expected an integer")
		}
	case reflect.Bool:
		// YAML 1.1 spellings such as yes/on and quoted strings must not be
		// coerced into bools by the struct decoder.
		var value bool
		if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" || node.Decode(&value) != nil {
			return invalid(path, "expected an explicit boolean (true or false)")
		}
	case reflect.Float64:
		var value float64
		if node.Kind != yaml.ScalarNode || (node.Tag != "!!float" && node.Tag != "!!int") || node.Decode(&value) != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return invalid(path, "expected a finite number")
		}
	default:
		return invalid(path, "unsupported schema type "+typ.String())
	}
	return nil
}
