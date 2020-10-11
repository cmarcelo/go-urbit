// SPDX-License-Identifier: MIT

package main

// This example shows how to interact with chat apps to send a
// message.  It makes use of the "chat" Go package that contains
// various types for reading chat information and preparing the
// message to send.
//
// It works by spawning an "event processor" goroutine while the main
// goroutine performs the calls and waits for results.
//
// The code was organized so that the flow can be read top to bottom
// instead of factored out into many functions, with generous use of
// comments.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/cmarcelo/go-urbit"
	"github.com/cmarcelo/go-urbit/chat"
	"github.com/cmarcelo/go-urbit/flagconfig"
)

func help() {
	fmt.Fprintf(os.Stderr, `chatsay: say something in an Urbit chat

Usage:
        chatsay --addr=ADDR --code=CODE
        chatsay --addr=ADDR --code=CODE --chat=CHAT --msg=MESSAGE
        chatsay --config=CONFIG [flags]

The --chat flag is used as a pattern, and will succeed if it matches a
single chat in the ship.  If not specified, the program will print a
list of available chats.

The --msg flag contains the message.  If not present, the message will
be read from standard input.  Empty messages are ignored.

The --config can be used to provide a file with the flags like in the
command line, except that in the file newlines can be used to separate
flags.  Command line flags take precedence.

The --trace flag prints information about each message sent and
received to the ship.

If not set, the address flag defaults to "http://localhost:8080" and
code flag defaults to the code for a fake ~zod ship.

`)
}

const fakeZodCode = "lidlut-tabwed-pillex-ridrup"

func main() {
	// Allow us to use return and defer for the main flow.
	err := mainErr()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %s\n", err)
		os.Exit(1)
	}
}

func mainErr() error {
	rand.Seed(time.Now().UnixNano())

	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.Usage = help
	var (
		flagAddr   = fs.String("addr", "http://localhost:8080", "")
		flagCode   = fs.String("code", fakeZodCode, "")
		flagChat   = fs.String("chat", "", "")
		flagMsg    = fs.String("msg", "", "")
		flagConfig = fs.String("config", "", "")
		flagTrace  = fs.Bool("trace", false, "")
	)

	err := flagconfig.Parse(fs, os.Args[1:], flagConfig)
	if err == flag.ErrHelp {
		return nil
	} else if err != nil {
		return err
	}

	// If we passed --chat, figure out what to send, either from
	// --msg or the standard input.
	var contents string
	if len(*flagChat) > 0 {
		usedFlagMsg := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "msg" {
				usedFlagMsg = true
			}
		})

		if usedFlagMsg {
			contents = *flagMsg
		} else {
			input, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			contents = strings.TrimSuffix(string(input), "\n")
		}
		if len(contents) == 0 {
			fmt.Fprintf(os.Stderr, "ignoring empty message")
			return nil
		}
	}

	opts := &urbit.DialOptions{Trace: *flagTrace}
	ship, err := urbit.Dial(*flagAddr, *flagCode, opts)
	if err != nil {
		return err
	}
	defer ship.Close()

	// Find out the list of available chats by subscribing to
	// chat-store.  The Subscribe function returns an ID, that we
	// can use later when processing events.
	//
	// The chat-store /keys subscription will generate two Events,
	// one "subscribe" with the result whether the subscription
	// was successful or not, and one "diff" with the actual
	// contents we are querying.
	keys := ship.Subscribe("chat-store", "/keys")
	if keys.Err != nil {
		return err
	}

	// Start a goroutine to listen to events.  Use the channels to
	// get results from those events.
	//
	// The only event we really care here is the list of chats.
	// Other events we don't handle will either be ignored (like
	// other diffs) or automatically trigger any Wait() call on
	// the results (see below for poke).
	keysCh := make(chan []string)
	errCh := make(chan error)

	go func() {
		for ev := range ship.Events() {
			switch {
			case ev.Type == "diff" && ev.ID == keys.ID:
				update, err := chat.ParseUpdate(ev.Data)
				if err != nil {
					errCh <- err
				} else if update.Keys == nil {
					errCh <- fmt.Errorf("missing keys attribute in chat-update")
				} else {
					keysCh <- *update.Keys
				}
			}
		}
	}()

	// We need to wait here, either for the keys
	var chats []string
	select {
	case chats = <-keysCh:
		// Got the chats!
	case err := <-errCh:
		return err
	}

	// If no chat flag was passed, just list the chats we've got.
	if len(*flagChat) == 0 {
		fmt.Printf("# ~%s is subscribed to %d chats:\n", ship.Name(), len(chats))
		for _, c := range chats {
			fmt.Println(c)
		}
		return nil
	}

	// Find the right chat based on the flag.
	var path string
	var matches []string
	for _, c := range chats {
		if strings.Contains(c, *flagChat) {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 0:
		return fmt.Errorf("no chats matching %q found in ~%s", *flagChat, ship.Name())
	case 1:
		path = matches[0]
	default:
		return fmt.Errorf("multiple options for chat pattern %q found in ~%s:\n%s", *flagChat, ship.Name(), strings.Join(matches, "\n"))
	}

	// Construct a Message to send.
	m := chat.Message{
		Path: path,
		Envelope: chat.Envelope{
			UID:    chat.GenerateUID(),
			Author: "~" + ship.Name(),
			When:   float64(time.Now().UnixNano() / 1_000_000),
			Letter: chat.Letter{
				Text: &contents,
			},
		},
	}

	// Wrap message in an object with the 'message' field.
	b, err := json.Marshal(struct {
		Message chat.Message `json:"message"`
	}{m})
	if err != nil {
		return err
	}

	// Send a POKE to chat-hook app and block waiting for the
	// result. As long as the events are being consumed, the
	// client will automatically unblock wait, returning either
	// nil or error.
	result := ship.Poke("chat-hook", json.RawMessage(b))
	return result.Wait()
}
