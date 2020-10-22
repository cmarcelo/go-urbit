// SPDX-License-Identifier: MIT

package main

// This example shows how to interact with chat apps to collect
// logs. It makes use of the "chat" Go package that contains various
// types for parsing JSON data from Urbit's chat-store and chat-view.
//
// It works a "batch" style (step by step), so it can consume Events()
// for each step.  More interactive programs would use a different
// Events() consuming approach.
//
// The code was organized so that the flow can be read top to bottom
// instead of factored out into many functions, with generous use of
// comments.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"

	"github.com/cmarcelo/go-urbit"
	"github.com/cmarcelo/go-urbit/chat"
	"github.com/cmarcelo/go-urbit/flagconfig"
)

func help() {
	fmt.Fprintf(os.Stderr, `chatlog: reads chats from an Urbit ship

Usage:
        chatlog --addr=ADDR --code=CODE
        chatlog --addr=ADDR --code=CODE --chat=CHAT [--follow]
        chatlog --config=CONFIG [flags]

Talks to an Urbit ship using HTTP and collect the current log for a
chat channel.

The --chat flag is used as a pattern, and will succeed if it matches a
single chat in the ship.  If not specified, the program will print a
list of available chats.

The --follow flag keeps the connection open, watching for new
messages.

The --config can be used to provide a file with the flags like in the
command line, except that in the file newlines can be used to separate
flags.  Command line flags take precedence.

The --details flag prints extra information in the logs: Number, When
and UID.

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
	// Parse command line.
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.Usage = help
	var (
		flagAddr    = fs.String("addr", "http://localhost:8080", "")
		flagCode    = fs.String("code", fakeZodCode, "")
		flagChat    = fs.String("chat", "", "")
		flagFollow  = fs.Bool("follow", false, "")
		flagConfig  = fs.String("config", "", "")
		flagDetails = fs.Bool("details", false, "")
		flagTrace   = fs.Bool("trace", false, "")
	)
	err := flagconfig.Parse(fs, os.Args[1:], flagConfig)
	if err == flag.ErrHelp {
		return nil
	} else if err != nil {
		return err
	}

	// Connect to the ship.
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
	var chats []string
	keys := ship.Subscribe("chat-store", "/keys")
	if keys.Err != nil {
		return err
	}
keysLoop:
	for ev := range ship.Events() {
		// An Event can have different types.  In this the
		// type we care most is "diff" that contains the
		// result from our request.
		switch {
		case ev.Type == urbit.ClientError:
			// These are errors that don't break the Event stream, so
			// we just print and move on.
			fmt.Fprintf(os.Stderr, "ERROR: %s\n", *ev.Err)

		case ev.Type == "subscribe" && ev.ID == keys.ID:
			// If Err is set, the subscription failed.
			if ev.Err != nil {
				return fmt.Errorf("subscription failed: %s", *ev.Err)
			}

		case ev.Type == "diff" && ev.ID == keys.ID:
			// The Event Data contains a JSON message with
			// the chat-update object with a keys attribute.
			update, err := chat.ParseUpdate(ev.Data)
			if err != nil {
				return err
			}
			if update.Keys == nil {
				return fmt.Errorf("missing keys attribute in chat-update")
			}
			chats = *update.Keys
			break keysLoop
		}

	}
	ship.Unsubscribe(keys.ID)

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
		return fmt.Errorf("multiple options for chat pattern  %q found in ~%s:\n%s", *flagChat, ship.Name(), strings.Join(matches, "\n"))
	}

	// Prepare requests to subscribe to
	//
	// - chat-store /mailbox/CHAT: that will have any new messages
	//   after the subscription.  This will produce "Message"
	//   chat events.
	//
	// - chat-view /primary: that will give us basic information
	//   about all the chats.  The key information we care is how
	//   many messages the chat has.  Unlike the subscription
	//   above at the moment there's no easy way to get
	//   information about just a single chat.  This will produce
	//   "Initial" chat events.
	reqs := []*urbit.Request{
		{
			Action: urbit.ActionSubscribe,
			Ship:   ship.Name(),
			App:    "chat-store",
			Path:   "/mailbox" + path,
		},
		{
			Action: urbit.ActionSubscribe,
			Ship:   ship.Name(),
			App:    "chat-view",
			Path:   "/primary",
		},
	}

	// If we don't want to --follow the chat for new messages,
	// drop the first subscription.
	if !*flagFollow {
		reqs = reqs[1:]
	}

	// Dispatch the results. This is the function used by the
	// simpler functions like Poke() and Subscribe().
	results := ship.DoMany(reqs)

	var updates, initial urbit.Result
	if !*flagFollow {
		initial = results[0]
		// Use updates with zero value, the ID zero will not
		// match any event and the err nil will not cause
		// the program to fail.
	} else {
		updates = results[0]
		initial = results[1]
	}

	if initial.Err != nil {
		return initial.Err
	}

	if updates.Err != nil {
		return updates.Err
	}

	// Consume events until we get the initial mailbox.  While
	// unlikely, if we get a new envelope (message) before the
	// initial mailbox, keep it in the pending list.
	var pending []chat.Envelope
	var mailbox *chat.Mailbox
	for ev := range ship.Events() {
		switch {
		case ev.Type == urbit.ClientError:
			fmt.Fprintf(os.Stderr, "ERROR: %s\n", *ev.Err)

		case ev.Type == "subscription":
			if ev.Err != nil {
				return fmt.Errorf("subscription failed: %w", *ev.Err)
			}

		case ev.Type == "diff" && ev.ID == initial.ID:
			mailbox, err = parseInitialMailbox(ev.Data, path)
			if err != nil {
				return err
			}

		case ev.Type == "diff" && ev.ID == updates.ID:
			update, err := chat.ParseUpdate(ev.Data)
			if err != nil {
				return err
			}
			if update.Message != nil {
				pending = append(pending, update.Message.Envelope)
			}
		}

		if mailbox != nil {
			break
		}
	}

	// The "/primary" subscription would keep sending us new
	// messages for all chats, but we don't need since we are
	// subscribed to the "/mailbox/CHAT" explained above.
	ship.Unsubscribe(initial.ID)

	// Chat-view provides a way to collect old messages by number,
	// we need to grab the entire backlog.  We split this work in
	// smaller steps so Eyre can cope with it.
	var envelopes []chat.Envelope
	backlog := mailbox.Config.Length
	for backlog > 0 {
		count := uint64(50)
		if backlog < count {
			count = backlog
		}
		data, err := ship.GetJSON(fmt.Sprintf("/chat-view/paginate/%d/%d%s", backlog-count, backlog, path))
		if err != nil {
			return err
		}
		update, err := chat.ParseUpdate(data)
		if err != nil {
			return err
		}
		if update.Messages != nil {
			envelopes = append(envelopes, update.Messages.Envelopes...)
		}
		backlog = backlog - count
	}

	// Order the messages from the older to the newer, which is
	// the opposite of what pagination provides us.
	sort.Slice(envelopes, func(i, j int) bool {
		return envelopes[i].Number < envelopes[j].Number
	})

	for _, e := range envelopes {
		printEnvelope(&e, *flagDetails)
	}

	// If we don't want to --follow the chat for more messages, we
	// are done now.
	if !*flagFollow {
		return nil
	}

	// The first time we processed events, we might got messages
	// before the initial mailbox, so we skip them here.
	for _, e := range pending {
		if e.Number > mailbox.Config.Length {
			printEnvelope(&e, *flagDetails)
		}
	}

	// Because the program will block until new messages are
	// available, create a goroutine to watch for interruption
	// (usually Ctrl-C), and close the connection with the ship
	// when that happens.
	//
	// While the ship can cope and after a while cleanup after a
	// client disappears, it is good to properly notify it about
	// it.
	go func() {
		ch := make(chan os.Signal)
		signal.Notify(ch, os.Interrupt)
		<-ch

		// This will cause the Events() channel to close,
		// stopping the processing below.
		ship.Close()
	}()

	// Keep listening for more events: either messages or a
	// notification that the chat was deleted.
followLoop:
	for ev := range ship.Events() {
		switch {
		case ev.Type == urbit.ClientError:
			fmt.Fprintf(os.Stderr, "ERROR: %s\n", *ev.Err)

		case ev.Type == "diff" && ev.ID == updates.ID:
			u, err := chat.ParseUpdate(ev.Data)
			if err != nil {
				return err
			}
			switch {
			case u.Message != nil:
				printEnvelope(&u.Message.Envelope, *flagDetails)
			case u.Delete != nil:
				break followLoop
			}
		}
	}

	return nil
}

// parseInitialMailbox will parse a JSON byte array from
// chat-store/all first "diff" message, that contains the initial
// state of all chats, and return the Mailbox for the passed chat.
func parseInitialMailbox(data []byte, path string) (*chat.Mailbox, error) {
	// Because this message contains the log of all channels,
	// first let's find out the subset for the chat we care about.
	//
	// Using json.RawMessage instead of the type we expect inside
	// as map value will prevent parsing inside the value to
	// happen.
	var j struct {
		Update struct {
			Initial map[string]json.RawMessage
		} `json:"chat-update"`
	}

	err := json.Unmarshal(data, &j)
	if err != nil {
		return nil, err
	}

	// We have just a json.RawMessage (which is a byte array),
	// need to parse it again as a Mailbox.
	mailboxData := j.Update.Initial[path]

	var mailbox chat.Mailbox

	err = json.Unmarshal(mailboxData, &mailbox)
	if err != nil {
		return nil, err
	}

	return &mailbox, nil
}

// printEnvelope will take an Envelope, a single communication from a
// ship in the chat, usually a line of text or URL and print it.
func printEnvelope(e *chat.Envelope, details bool) {
	if details {
		fmt.Printf("%05d %.0f %s ", e.Number, e.When, e.UID)
	}
	switch {
	case e.Letter.Text != nil:
		fmt.Printf("~%s> %s\n", e.Author, *e.Letter.Text)
	case e.Letter.URL != nil:
		fmt.Printf("~%s> %s\n", e.Author, *e.Letter.URL)
	case e.Letter.Me != nil:
		fmt.Printf("~%s %s\n", e.Author, *e.Letter.Me)
	case e.Letter.Code != nil:
		fmt.Printf("~%s CODE> %s\n", e.Author, e.Letter.Code.Expression)
		for _, o := range e.Letter.Code.Output {
			fmt.Printf("~%s OUTPUT> %s\n", e.Author, strings.Join(o, " "))
		}
	}
}
