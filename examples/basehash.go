// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/cmarcelo/go-urbit"
	"github.com/cmarcelo/go-urbit/flagconfig"
)

func help() {
	fmt.Fprintf(os.Stderr, `basehash: prints the base hash for an Urbit ship

Usage:
        basehash --addr=ADDR --code=CODE
        basehash --config=CONFIG [flags]

Talks to an Urbit ship to collect its base hash.

The --config can be used to provide a file with the flags like in the
command line, except that in the file newlines can be used to separate
flags.  Command line flags take precedence.

If not set, the address flag defaults to "http://localhost:8080" and
code flag defaults to the code for a fake ~zod ship.

`)
}

const fakeZodCode = "lidlut-tabwed-pillex-ridrup"

func fail(err error) {
	fmt.Fprintf(os.Stderr, "FAIL: %s\n", err)
	os.Exit(1)
}

func main() {
	// Parse command line.
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.Usage = help
	var (
		flagAddr   = fs.String("addr", "http://localhost:8080", "")
		flagCode   = fs.String("code", fakeZodCode, "")
		flagConfig = fs.String("config", "", "")
	)
	err := flagconfig.Parse(fs, os.Args[1:], flagConfig)
	if err == flag.ErrHelp {
		return
	} else if err != nil {
		fail(err)
	}

	// Connect to the ship.
	ship, err := urbit.Dial(*flagAddr, *flagCode, nil)
	if err != nil {
		fail(err)
	}

	// The base hash is currently exposed as a value that can be
	// queried via a Scry.  Note these queries do not change the
	// state of the ship.
	quotedHash, err := ship.Scry("file-server", "/clay/base/hash")
	if err != nil {
		fail(err)
	}

	// We are done with the ship.
	ship.Close()

	hash, err := strconv.Unquote(string(quotedHash))
	if err != nil {
		fail(err)
	}

	fmt.Printf("ship name: ~%s\n", string(ship.Name()))
	fmt.Printf("base hash: %s\n", hash)
}
