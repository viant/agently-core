package tablepreference

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const ConfigPath = "extension/forge/preferences.yaml"

type Tools struct {
	Get   string `json:"get" yaml:"get"`
	Set   string `json:"set" yaml:"set"`
	Reset string `json:"reset" yaml:"reset"`
}

// MCP selects an existing host-configured server, never a credential or URL.
type MCP struct {
	ServerRef string `json:"serverRef" yaml:"serverRef"`
	Tools     Tools  `json:"tools" yaml:"tools"`
}

type AdapterConfig struct {
	Adapter   string `json:"adapter" yaml:"adapter"`
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	MCP       *MCP   `json:"mcp,omitempty" yaml:"mcp,omitempty"`
}

type Config struct {
	Version          int           `json:"version" yaml:"version"`
	TablePreferences AdapterConfig `json:"tablePreferences" yaml:"tablePreferences"`
}

func DefaultConfig() Config {
	return Config{Version: 1, TablePreferences: AdapterConfig{Adapter: "browser"}}
}

func (c *Config) Validate() error {
	if c.Version == 0 {
		c.Version = 1
	}
	if c.Version != 1 {
		return fmt.Errorf("table preferences config: unsupported version")
	}
	p := &c.TablePreferences
	if p.Adapter == "" {
		p.Adapter = "browser"
	}
	if p.Namespace != "" {
		if err := identifier("namespace", p.Namespace, 256); err != nil {
			return err
		}
	}
	switch p.Adapter {
	case "browser":
		if p.MCP != nil {
			return fmt.Errorf("table preferences config: browser adapter cannot declare MCP tools")
		}
	case "mcp":
		if p.MCP == nil {
			return fmt.Errorf("table preferences config: MCP configuration is required")
		}
		for name, value := range map[string]string{"serverRef": p.MCP.ServerRef, "get tool": p.MCP.Tools.Get, "set tool": p.MCP.Tools.Set, "reset tool": p.MCP.Tools.Reset} {
			if err := identifier(name, value, 256); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("table preferences config: unsupported adapter")
	}
	return nil
}

// LoadConfig reads a workspace's optional declaration. Missing configuration
// selects browser storage; malformed explicit configuration fails visibly.
func LoadConfig(workspaceRoot string) (Config, error) {
	c := DefaultConfig()
	f, err := os.Open(filepath.Join(workspaceRoot, filepath.FromSlash(ConfigPath)))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return c, err
	}
	if len(b) > MaxBytes {
		return c, fmt.Errorf("table preferences config: file exceeds size limit")
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return c, fmt.Errorf("table preferences config: multiple documents are not supported")
	}
	return c, c.Validate()
}
