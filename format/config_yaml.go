//go:build !glimt_nodeps

package format

import (
	"bytes"
	"errors"
	"io"

	"gopkg.in/yaml.v3"
)

// decodeConfig parses YAML strictly: unknown keys are errors, so a typo
// like "regx:" is reported (with its line) instead of silently ignored.
func decodeConfig(data []byte, c *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) { // EOF: empty file
		return err
	}
	return nil
}
