package otelinject

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const DefaultOBIConfigPath = "/etc/obi-agent/config.yaml"

// OBISelector represents one entry under discovery.instrument in OBI config.
// Field names and yaml tags match OBI's GlobAttributes schema.
type OBISelector struct {
	Name           string `yaml:"name,omitempty"`
	OpenPorts      string `yaml:"open_ports,omitempty"`
	ExePath        string `yaml:"exe_path,omitempty"`
	CmdArgs        string `yaml:"cmd_args,omitempty"`
	Languages      string `yaml:"languages,omitempty"`
	ContainersOnly bool   `yaml:"containers_only,omitempty"`
}

// OBIConfig wraps a yaml.Node tree so reads and writes preserve comments,
// ordering, and sections we don't touch.
type OBIConfig struct {
	root yaml.Node
}

// ReadOBIConfig parses an OBI config file into a round-trip-safe structure.
func ReadOBIConfig(path string) (*OBIConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read obi config: %w", err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse obi config: %w", err)
	}

	return &OBIConfig{root: root}, nil
}

// NewEmptyOBIConfig returns an OBIConfig with an empty document structure.
func NewEmptyOBIConfig() *OBIConfig {
	doc := yaml.Node{Kind: yaml.DocumentNode}
	mapping := yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	doc.Content = append(doc.Content, &mapping)
	return &OBIConfig{root: doc}
}

// AddSelector adds or overwrites a selector in discovery.instrument[].
// Returns true if an existing selector was overwritten.
func (c *OBIConfig) AddSelector(sel OBISelector) (overwritten bool, err error) {
	seq, err := c.ensureInstrumentSeq()
	if err != nil {
		return false, err
	}

	// Check for existing entry with same name
	if sel.Name != "" {
		for i, node := range seq.Content {
			if selectorName(node) == sel.Name {
				replacement, err := selectorToNode(sel)
				if err != nil {
					return false, err
				}
				seq.Content[i] = replacement
				return true, nil
			}
		}
	}

	node, err := selectorToNode(sel)
	if err != nil {
		return false, err
	}
	seq.Content = append(seq.Content, node)
	return false, nil
}

// RemoveSelector removes the selector with the given name. Returns true if found.
func (c *OBIConfig) RemoveSelector(name string) bool {
	seq := c.findInstrumentSeq()
	if seq == nil {
		return false
	}
	for i, node := range seq.Content {
		if selectorName(node) == name {
			seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
			return true
		}
	}
	return false
}

// HasSelector returns true if a selector with the given name exists.
func (c *OBIConfig) HasSelector(name string) bool {
	seq := c.findInstrumentSeq()
	if seq == nil {
		return false
	}
	for _, node := range seq.Content {
		if selectorName(node) == name {
			return true
		}
	}
	return false
}

// ListSelectors returns all selectors under discovery.instrument[].
func (c *OBIConfig) ListSelectors() []OBISelector {
	seq := c.findInstrumentSeq()
	if seq == nil {
		return nil
	}
	var result []OBISelector
	for _, node := range seq.Content {
		var sel OBISelector
		if err := node.Decode(&sel); err == nil {
			result = append(result, sel)
		}
	}
	return result
}

// Write atomically writes the config to path (write temp + rename).
func (c *OBIConfig) Write(path string) error {
	data, err := yaml.Marshal(&c.root)
	if err != nil {
		return fmt.Errorf("marshal obi config: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".obi-config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Preserve original file permissions if it exists
	if info, err := os.Stat(path); err == nil {
		os.Chmod(tmpPath, info.Mode())
	} else {
		os.Chmod(tmpPath, 0644)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp to config: %w", err)
	}
	return nil
}

// --- yaml.Node helpers ---

// docRoot returns the top-level mapping node inside the document.
func (c *OBIConfig) docRoot() *yaml.Node {
	if c.root.Kind == yaml.DocumentNode && len(c.root.Content) > 0 {
		return c.root.Content[0]
	}
	return nil
}

// findMappingKey returns the value node for a key in a mapping node.
func findMappingKey(mapping *yaml.Node, key string) *yaml.Node {
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

// addMappingKey appends a key-value pair to a mapping node.
func addMappingKey(mapping *yaml.Node, key string, value *yaml.Node) {
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	mapping.Content = append(mapping.Content, keyNode, value)
}

// findInstrumentSeq returns the discovery.instrument sequence node, or nil.
func (c *OBIConfig) findInstrumentSeq() *yaml.Node {
	disc := findMappingKey(c.docRoot(), "discovery")
	if disc == nil {
		return nil
	}
	return findMappingKey(disc, "instrument")
}

// ensureInstrumentSeq returns or creates the discovery.instrument sequence node.
func (c *OBIConfig) ensureInstrumentSeq() (*yaml.Node, error) {
	root := c.docRoot()
	if root == nil {
		return nil, fmt.Errorf("obi config has no root mapping")
	}

	disc := findMappingKey(root, "discovery")
	if disc == nil {
		disc = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		addMappingKey(root, "discovery", disc)
	}

	seq := findMappingKey(disc, "instrument")
	if seq == nil {
		seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		addMappingKey(disc, "instrument", seq)
	}

	return seq, nil
}

// selectorToNode marshals an OBISelector into a yaml.Node.
func selectorToNode(sel OBISelector) (*yaml.Node, error) {
	var node yaml.Node
	if err := node.Encode(sel); err != nil {
		return nil, err
	}
	return &node, nil
}

// selectorName extracts the "name" field from a mapping node.
func selectorName(node *yaml.Node) string {
	if node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "name" {
			return node.Content[i+1].Value
		}
	}
	return ""
}
