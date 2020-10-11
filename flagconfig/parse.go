// SPDX-License-Identifier: MIT

package flagconfig

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"strings"
)

// TODO: This was originally a shared parse function for the various
// examples, moved to a package to make my life easier.  Need some API
// review and figure out a way to play nice with the various flag
// error behaviours.

// Parse provides support for reading flags from a configuration file
// in addition of the command line.  The filename is passed as a
// pointer so its name can be parsed from the command line as a flag.
//
// NOTE: The FlagSet must use ContinueOnError.
func Parse(fs *flag.FlagSet, arguments []string, flagConfig *string) error {
	// TODO: Can we do it without depending on this?
	if fs.ErrorHandling() != flag.ContinueOnError {
		return fmt.Errorf("flagconfig: Wrong ErrorHandling for FlagSet, must use flag.ContinueOnError")
	}
	err := fs.Parse(arguments)
	if err != nil {
		return err
	}

	// There is a configuration file.
	if len(*flagConfig) > 0 {
		var config []byte
		config, err = ioutil.ReadFile(*flagConfig)
		if err != nil {
			return err
		}
		config = bytes.TrimSpace(config)
		err = fs.Parse(strings.Fields(string(config)))
		if err != nil {
			return err
		}

		// TODO: Do this with two instead of three runs.

		// Redo the parse from the command line to make sure that gets
		// precedence.
		err = fs.Parse(arguments)
		if err != nil {
			return err
		}
	}

	return nil
}
