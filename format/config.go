package format

import (
	"fmt"
	"os"
	"path/filepath"
)

// Config is the user config file. Its only job is defining extra formats:
//
//	# ~/.config/glimt/config.yaml
//	formats:
//	  - name: myapp
//	    regex: '^(?P<ts>\S+) \[(?P<level>\w+)\] (?P<msg>.*?) took=(?P<took>\S+)$'
//	    graph: took
//
// User formats are tried before the built-in ones during detection.
type Config struct {
	Formats []RegexSpec `yaml:"formats"`
}

// ConfigPath is where the config is looked up when no path is given:
// $XDG_CONFIG_HOME/glimt/config.yaml (os.UserConfigDir), then
// ~/.config/glimt/config.yaml, which is where most macOS devs expect it.
func ConfigPath() string {
	var candidates []string
	if dir, err := os.UserConfigDir(); err == nil {
		candidates = append(candidates, filepath.Join(dir, "glimt", "config.yaml"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "glimt", "config.yaml"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[len(candidates)-1]
	}
	return ""
}

// LoadConfig reads and validates a config file. With path == "" a missing
// file is fine (no config); an explicitly given path must exist.
func LoadConfig(path string) (Config, error) {
	explicit := path != ""
	if !explicit {
		path = ConfigPath()
	}
	var c Config
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			return c, nil
		}
		return c, err
	}
	if err := decodeConfig(data, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	for i, f := range c.Formats {
		switch {
		case f.Name == "":
			return c, fmt.Errorf("%s: formats[%d]: missing name", path, i)
		case f.Expr == "":
			return c, fmt.Errorf("%s: format %q: missing regex", path, f.Name)
		case seen[f.Name]:
			return c, fmt.Errorf("%s: format %q defined twice", path, f.Name)
		}
		seen[f.Name] = true
		if _, err := f.Compile(); err != nil {
			return c, fmt.Errorf("%s: %w", path, err)
		}
	}
	return c, nil
}
