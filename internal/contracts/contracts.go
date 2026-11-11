// Package contracts validates the request and response JSON documents.
package contracts

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed schemas/*.json
var schemaFiles embed.FS

var (
	requestOnce  sync.Once
	requestDoc   *jsonschema.Schema
	requestErr   error
	responseOnce sync.Once
	responseDoc  *jsonschema.Schema
	responseErr  error
)

func compile(name string) (*jsonschema.Schema, error) {
	text, err := schemaFiles.ReadFile("schemas/" + name)
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(name, bytes.NewReader(text)); err != nil {
		return nil, err
	}
	return compiler.Compile(name)
}

func requestSchema() (*jsonschema.Schema, error) {
	requestOnce.Do(func() { requestDoc, requestErr = compile("request.schema.json") })
	return requestDoc, requestErr
}

func responseSchema() (*jsonschema.Schema, error) {
	responseOnce.Do(func() { responseDoc, responseErr = compile("response.schema.json") })
	return responseDoc, responseErr
}

// ValidateRequest validates the outer request envelope.
func ValidateRequest(payload map[string]any) error {
	schema, err := requestSchema()
	if err != nil {
		return err
	}
	normalized, err := normalize(payload)
	if err != nil {
		return err
	}
	if err := schema.Validate(normalized); err != nil {
		return formatValidationError(err)
	}
	return nil
}

// ValidateResponse validates the response before it is written.
func ValidateResponse(payload map[string]any) error {
	schema, err := responseSchema()
	if err != nil {
		return err
	}
	normalized, err := normalize(payload)
	if err != nil {
		return err
	}
	if err := schema.Validate(normalized); err != nil {
		return formatValidationError(err)
	}
	return nil
}

func normalize(payload map[string]any) (any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func formatValidationError(err error) error {
	var validation *jsonschema.ValidationError
	if !errors.As(err, &validation) {
		return err
	}
	first := validation
	for len(first.Causes) > 0 {
		first = first.Causes[0]
	}
	location := strings.TrimPrefix(first.InstanceLocation, "/")
	location = strings.ReplaceAll(location, "/", ".")
	if location == "" {
		location = "<root>"
	}
	return errors.New(location + ": " + first.Message)
}
