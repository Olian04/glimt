//go:build glimt_nodeps

package format

import "errors"

// Built with -tags glimt_nodeps (no YAML library available): config files
// can't be read. Only used to build and test in environments without
// module downloads.
func decodeConfig([]byte, *Config) error {
	return errors.New("this build has no YAML support (built with -tags glimt_nodeps)")
}
