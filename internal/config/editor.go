package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/ward/ward/internal/state"
	"gopkg.in/yaml.v3"
)

// AddFileLogSource updates only the logs.sources portion of a project YAML.
// Editing the YAML node tree preserves comments and unrelated configuration.
func AddFileLogSource(path, environmentID string, source LogSourceConfig) error {
	cfg, err := LoadFile(path)
	if err != nil {
		return err
	}
	env, ok := cfg.Environments[environmentID]
	if !ok {
		return fmt.Errorf("environment %q is not configured", environmentID)
	}
	for _, existing := range env.Logs.Sources {
		if existing.ID == source.ID {
			return fmt.Errorf("log source %q already exists", source.ID)
		}
	}
	env.Logs.Sources = append(env.Logs.Sources, source)
	cfg.Environments[environmentID] = env
	if err := Validate(cfg); err != nil {
		return fmt.Errorf("validate updated configuration: %w", err)
	}

	document, err := readYAMLDocument(path)
	if err != nil {
		return err
	}
	root := document.Content[0]
	environments := mappingValue(root, "environments")
	if environments == nil || environments.Kind != yaml.MappingNode {
		return fmt.Errorf("configuration environments mapping is missing")
	}
	environment := mappingValue(environments, environmentID)
	if environment == nil || environment.Kind != yaml.MappingNode {
		return fmt.Errorf("environment %q mapping is missing", environmentID)
	}
	logsNode := mappingValue(environment, "logs")
	if logsNode == nil {
		logsNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingValue(environment, "logs", logsNode)
	}
	sources := mappingValue(logsNode, "sources")
	if sources == nil {
		sources = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		setMappingValue(logsNode, "sources", sources)
	}
	sourceNode, err := nodeFor(source)
	if err != nil {
		return fmt.Errorf("encode log source: %w", err)
	}
	sources.Content = append(sources.Content, sourceNode)
	return writeYAMLDocument(path, document)
}

// RemoveLogSource removes a source by ID and leaves an empty sources sequence
// when it was the last configured source.
func RemoveLogSource(path, environmentID, sourceID string) error {
	cfg, err := LoadFile(path)
	if err != nil {
		return err
	}
	env, ok := cfg.Environments[environmentID]
	if !ok {
		return fmt.Errorf("environment %q is not configured", environmentID)
	}
	index := -1
	for i, source := range env.Logs.Sources {
		if source.ID == sourceID {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("log source %q is not configured", sourceID)
	}
	env.Logs.Sources = append(env.Logs.Sources[:index], env.Logs.Sources[index+1:]...)
	cfg.Environments[environmentID] = env
	if err := Validate(cfg); err != nil {
		return fmt.Errorf("validate updated configuration: %w", err)
	}

	document, err := readYAMLDocument(path)
	if err != nil {
		return err
	}
	environments := mappingValue(document.Content[0], "environments")
	environment := mappingValue(environments, environmentID)
	logsNode := mappingValue(environment, "logs")
	sources := mappingValue(logsNode, "sources")
	if sources == nil || sources.Kind != yaml.SequenceNode {
		return fmt.Errorf("environment %q logs.sources mapping is missing", environmentID)
	}
	filtered := sources.Content[:0]
	removed := false
	for _, item := range sources.Content {
		if mappingScalarValue(item, "id") == sourceID {
			removed = true
			continue
		}
		filtered = append(filtered, item)
	}
	if !removed {
		return fmt.Errorf("log source %q is not configured", sourceID)
	}
	sources.Content = filtered
	return writeYAMLDocument(path, document)
}

func readYAMLDocument(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("configuration root must be a YAML mapping")
	}
	return &document, nil
}

func writeYAMLDocument(path string, document *yaml.Node) error {
	data, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return state.AtomicWrite(path, data, 0o600)
}

func nodeFor(value any) (*yaml.Node, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 {
		return nil, fmt.Errorf("empty YAML node")
	}
	return document.Content[0], nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func mappingScalarValue(mapping *yaml.Node, key string) string {
	value := mappingValue(mapping, key)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(value.Value)
}

func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}
