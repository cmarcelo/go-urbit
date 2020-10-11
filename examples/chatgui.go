// SPDX-License-Identifier: MIT

package main

// Rudimentary GUI chat client, that interacts with the chat apps to
// receive and send messages.  Unlike other examples this is rather
// incomplete and more an experiment, there are "TODO" comments around
// the code and a larger section by the end of the file.
//
// The gioui library for GUI was chosen because it's wide platform
// support, which includes mobile.  It uses an "immediate mode"
// approach for constructing UIs that might not be familiar but should
// be straightforward.
//
// The code was organized so that the flow can be read top to bottom
// instead of factored out into many functions, with generous use of
// comments.

import (
	"encoding/json"
	"flag"
	"fmt"
	"image/color"
	"math/rand"
	"os"
	"sort"
	"time"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/cmarcelo/go-urbit"
	"github.com/cmarcelo/go-urbit/chat"
	"github.com/cmarcelo/go-urbit/flagconfig"
)

func help() {
	fmt.Fprintf(os.Stderr, `chatgui: graphical interface to Urbit chat

Usage:
        chatgui --addr=ADDR --code=CODE
        chatgui --config=CONFIG [flags]

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
	rand.Seed(time.Now().UnixNano())

	// TODO: Get rid of flag parsing and use GUI to configure the
	// address.  Also save a configuration file.
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.Usage = help
	var (
		flagAddr   = fs.String("addr", "http://localhost:8080", "")
		flagCode   = fs.String("code", fakeZodCode, "")
		flagConfig = fs.String("config", "", "")
		flagTrace  = fs.Bool("trace", false, "")
	)
	err := flagconfig.Parse(fs, os.Args[1:], flagConfig)
	if err == flag.ErrHelp {
		return
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing flags: %s", err)
		os.Exit(1)
	}

	// To work in all platforms, gioui needs to "take-over" the main
	// goroutine, so the main Loop is executed in a new goroutine.

	go func() {
		a, err := newApp(*flagAddr, *flagCode, *flagTrace)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: %s\n", err)
			os.Exit(1)
		}

		if err := a.Loop(); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: %s\n", err)
			os.Exit(1)
		}

		os.Exit(0)
	}()

	// Gioui Main function.
	app.Main()
}

type App struct {
	// Urbit connectivity.  For now only a Client.
	ship *urbit.Client

	// State for all chats, and a reference to the current chat
	// being used.
	store   map[string]*Chat
	current *Chat

	// State for the user interface.  These are the GUI objects
	// that need to be around for the entire execution of the
	// program.
	order []string          // List of chat paths sorted by name.
	win   *app.Window       // Window object of gioui.
	theme *material.Theme   // Theme object used when drawing.
	next  *widget.Clickable // "Click" state for next...
	prev  *widget.Clickable // ...and prev buttons.
	entry *widget.Editor    // State for the message entry field.
	list  layout.List       // State for the scrollback.

	// The program is organized so that certain actions
	// (e.g. sending a message) are started in goroutines and talk
	// back to the main Loop by feeding this channel.
	//
	// Both sets of states above are "owned" by the main Loop
	// goroutine, other goroutines are expected to use messages.
	appEvents chan AppEvent
}

type Chat struct {
	// The Path to a chat, e.g. /~zod/abc.
	Path string

	// Envelopes (roughly "messages") in a chat, sorted.
	Envelopes []chat.Envelope

	// Envelopes sent to the ship owning the chat but that still
	// haven't been received by the chat stream.  These are the
	// messages shown is a lighter color.
	PendingEnvelopes []chat.Envelope
}

// Events produced by helper goroutines.  Use an interface with a
// dummy method to effectively enumerate the valid Event types.
type (
	AppEvent                  interface{ appEvent() }
	AddPendingMessageEvent    struct{ Message *chat.Message }
	RemovePendingMessageEvent struct{ Message *chat.Message }
	AddMessageEvent           struct{ Message *chat.Message }
	AddMessagesEvent          struct{ Messages *chat.Messages }
	ErrorEvent                struct{ err error }
)

func (AddPendingMessageEvent) appEvent()    {}
func (RemovePendingMessageEvent) appEvent() {}
func (AddMessageEvent) appEvent()           {}
func (AddMessagesEvent) appEvent()          {}
func (ErrorEvent) appEvent()                {}

func newApp(addr, code string, trace bool) (*App, error) {
	// Connects to the ship.
	ship, err := urbit.Dial(addr, code, &urbit.DialOptions{Trace: trace})
	if err != nil {
		return nil, err
	}

	// Subscribes to chat-view's /primary stream.  This gives a
	// small initial set of messages for each chat, and send
	// updates for new messages.
	primary := ship.Subscribe("chat-view", "/primary")
	if primary.Err != nil {
		return nil, primary.Err
	}

	return &App{
		ship: ship,

		store: make(map[string]*Chat),

		appEvents: make(chan AppEvent),

		win:   app.NewWindow(app.Title("Chat GUI")),
		theme: material.NewTheme(gofont.Collection()),
		next:  &widget.Clickable{},
		prev:  &widget.Clickable{},
		entry: &widget.Editor{
			SingleLine: true,
			Submit:     true,
		},
		list: layout.List{
			Axis:        layout.Vertical,
			ScrollToEnd: true,
		},
	}, nil
}

// The main Loop of the program.  It waits for events from various
// sources and process them.
func (a *App) Loop() error {
	var ops op.Ops
	for {
		select {
		case e := <-a.win.Events():
			// Events from the GUI.
			switch e := e.(type) {
			case system.DestroyEvent:
				// Window was closed.
				a.exit()

			case system.FrameEvent:
				// New frame needs to be drawn.  This
				// happens either by request of the
				// window system (e.g. first time
				// screen is shown, a button is
				// clicked), or by the program itself
				// by using Invalidate call (e.g. new
				// message arrived).
				gtx := layout.NewContext(&ops, e)

				// Draw UI and handle UI events.
				a.ui(gtx, &e)
				e.Frame(gtx.Ops)
			}

		case e := <-a.ship.Events():
			// Events from Urbit.
			a.processShipEvent(e)

		case e := <-a.appEvents:
			// Finally, events from helper goroutines.
			a.processAppEvent(e)
		}
	}
}

// Helpers commonly used when using gioui, they make easier to write
// the types of layouting functions below.
type (
	D = layout.Dimensions
	C = layout.Context
)

// Both handle events and draw the next frame.  The events are peeked
// from state objects, and the next frame is drawn by adding
// operations to gtx.
//
// The layout and material packages provide various helpers that are
// used here.
func (a *App) ui(gtx layout.Context, e *system.FrameEvent) {
	if a.current == nil {
		return
	}

	// Either the next or previous button were clicked, so cycle
	// through the a.order and find the new current chat.
	if a.next.Clicked() || a.prev.Clicked() {
		var step int
		if a.next.Clicked() {
			step = +1
		} else {
			step = -1
		}

		cur := 0
		for i, s := range a.order {
			if s == a.current.Path {
				cur = i
				break
			}
		}
		cur = ((cur + step) + len(a.order)) % len(a.order)
		a.current = a.store[a.order[cur]]

		// TODO: When changing to a different chat, save the
		// chat scroll position.
	}

	// Handle events from the text entry field used to send new
	// messages.
	for _, ev := range a.entry.Events() {
		if ev, ok := ev.(widget.SubmitEvent); ok {
			// This happens when the user press ENTER or
			// similar.  Spawn a new goroutine to send the
			// message and empty the field.
			go a.sendText(a.current.Path, ev.Text)
			a.entry.SetText("")
		}
	}

	// Draw the UI elements using the layout helpers.  Behind the
	// scenes the effect of all these functions is to add drawing
	// operations into the gtx.
	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// TOP BAR.
		layout.Rigid(func(gtx C) D {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				// PREVIOUS BUTTON.
				layout.Rigid(func(gtx C) D {
					inset := layout.UniformInset(unit.Dp(10))
					return inset.Layout(gtx, material.Button(a.theme, a.prev, "<<").Layout)
				}),
				// CHAT PATH.
				layout.Flexed(1, func(gtx C) D {
					title := material.H3(a.theme, a.current.Path)
					title.Alignment = text.Middle
					return title.Layout(gtx)
				}),
				// NEXT BUTTON.
				layout.Rigid(func(gtx C) D {
					inset := layout.UniformInset(unit.Dp(10))
					return inset.Layout(gtx, material.Button(a.theme, a.next, ">>").Layout)
				}),
			)
		}),
		// CHAT CONTENTS.
		layout.Flexed(1, func(gtx C) D {
			count := len(a.current.Envelopes) + len(a.current.PendingEnvelopes)
			return a.list.Layout(gtx, count, func(gtx C, i int) D {
				var textColor color.RGBA
				var text string
				if i < len(a.current.Envelopes) {
					text = envelopeToString(a.current.Envelopes[i])
					textColor = a.theme.Color.Text
				} else {
					index := i - len(a.current.Envelopes)
					text = envelopeToString(a.current.PendingEnvelopes[index])
					textColor = a.theme.Color.Hint
				}
				l := material.Body1(a.theme, text)
				l.Color = textColor
				return l.Layout(gtx)
			})
		}),
		// TEXT ENTRY FIELD.
		layout.Rigid(func(gtx C) D {
			editor := material.Editor(a.theme, a.entry, "")
			border := widget.Border{Color: color.RGBA{A: 0xff}, CornerRadius: unit.Dp(8), Width: unit.Px(2)}
			return border.Layout(gtx, func(gtx C) D {
				return layout.UniformInset(unit.Dp(8)).Layout(gtx, editor.Layout)
			})
		}),
	)
}

func (a *App) sendText(path, text string) {
	// Create a new Envelope, note a new UID is generated.  This
	// will be used later to match a pending message with an
	// actual one received from the chat owner.
	m := chat.Message{
		Path: path,
		Envelope: chat.Envelope{
			UID:    chat.GenerateUID(),
			Author: "~" + a.ship.Name(),
			When:   float64(time.Now().UnixNano() / 1_000_000),
			Letter: chat.Letter{
				Text: &text,
			},
		},
	}
	b, err := json.Marshal(struct {
		Message chat.Message `json:"message"`
	}{m})
	if err != nil {
		a.appEvents <- ErrorEvent{err}
		return
	}

	// Tell the program that this envelope is pending.  Note
	// because this runs in a separate goroutine, it uses the
	// appEvents channel to do that.
	a.appEvents <- AddPendingMessageEvent{&m}

	// Send the message to the ship.  Unlike the rest of the
	// state, the Urbit client methods can be called from
	// different goroutines.
	r := a.ship.Poke("chat-hook", json.RawMessage(b))

	// It is fine to "block" here waiting for a response, because
	// this is a goroutine just to send this message.
	err = r.Wait()
	if err != nil {
		// A failure happened in the Poke, drop the pending
		// message and let the application know.
		a.appEvents <- RemovePendingMessageEvent{&m}
		a.appEvents <- ErrorEvent{err}
	}

	// If there's was no error, once the message is received from
	// the chat-store, the pending one will be removed.
}

func (a *App) processShipEvent(ev *urbit.Event) {
	switch ev.Type {
	case "subscribe":
		// TODO: Handle if there was a subscription error.

	case "diff":
		update, err := chat.ParseUpdate(ev.Data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "GOT ERROR: %s\n", *ev.Err)
			a.exit()
		}

		// Initial set of messages.
		if update.Initial != nil {
			for path, mailbox := range *update.Initial {
				a.processAppEvent(AddMessagesEvent{&chat.Messages{path, mailbox.Envelopes}})
			}
		}

		// Single new message arrived.
		if update.Message != nil {
			a.processAppEvent(AddMessageEvent{Message: update.Message})
		}

		// TODO: Handle other types of diffs.

	case urbit.ClientError:
		fmt.Fprintf(os.Stderr, "GOT ERROR: %s\n", *ev.Err)
		a.exit()
	}
}

func (a *App) processAppEvent(ev AppEvent) {
	switch ev := ev.(type) {
	case AddPendingMessageEvent:
		chat := a.getChat(ev.Message.Path)
		chat.PendingEnvelopes = append(chat.PendingEnvelopes, ev.Message.Envelope)

	case RemovePendingMessageEvent:
		chat := a.getChat(ev.Message.Path)
		chat.removePendingByUID(ev.Message.Envelope.UID)

	case AddMessageEvent:
		chat := a.getChat(ev.Message.Path)
		chat.removePendingByUID(ev.Message.Envelope.UID)

		// TODO: We want to insert at sorted position instead of re-sorting.
		chat.Envelopes = append(chat.Envelopes, ev.Message.Envelope)
		sort.Slice(chat.Envelopes, func(i, j int) bool {
			return chat.Envelopes[i].When < chat.Envelopes[j].When
		})

	case AddMessagesEvent:
		chat := a.getChat(ev.Messages.Path)

		// TODO: We want to insert at sorted position instead of re-sorting.
		chat.Envelopes = append(chat.Envelopes, ev.Messages.Envelopes...)
		sort.Slice(chat.Envelopes, func(i, j int) bool {
			return chat.Envelopes[i].When < chat.Envelopes[j].When
		})
	}

	// TODO: Could invalidate less by comparing chat to a.current.
	a.win.Invalidate()
}

func (a *App) getChat(path string) *Chat {
	chat, ok := a.store[path]
	if !ok {
		chat = &Chat{Path: path}
		a.store[path] = chat
		if a.current == nil {
			a.current = chat
		}
		a.order = append(a.order, path)
		sort.Strings(a.order)
	}
	return chat
}

func (c *Chat) removePendingByUID(uid string) {
	drop := -1
	for i, envelope := range c.PendingEnvelopes {
		if envelope.UID == uid {
			drop = i
			break
		}
	}

	if drop > -1 {
		// TODO: Proper removal to avoid misusing the underlying slice.
		pending := c.PendingEnvelopes
		c.PendingEnvelopes = append(pending[:drop], pending[drop+1:]...)
	}
}

func (a *App) exit() {
	a.ship.Close()
	os.Exit(0)
}

func envelopeToString(e chat.Envelope) string {
	// Normalize Author.  Outbound envelopes use sig but inbound
	// don't.  For the UI we want the sig, so add it.
	author := e.Author
	if author[0] != '~' {
		author = "~" + e.Author
	}

	switch {
	case e.Letter.Text != nil:
		return fmt.Sprintf("%s> %s\n", author, *e.Letter.Text)
	case e.Letter.URL != nil:
		return fmt.Sprintf("%s> %s\n", author, *e.Letter.URL)
	case e.Letter.Me != nil:
		return fmt.Sprintf("%s %s\n", author, *e.Letter.Me)
	case e.Letter.Code != nil:
		// TODO: Include output.
		return fmt.Sprintf("%s CODE> %s\n", author, e.Letter.Code.Expression)
	}

	return ""
}

// TODO
//
// - Add a "Get more backlog" button.  Chat provides paginated
// data via HTTP GET in /chat-view/paginate/NNN/MMM/CHAT_PATH.
//
// - Setup connection via UI and save config file, so this can be
// used in mobile and we can drop the command line parsing.
//
// - Comment we are ignoring a bunch of stuff, including "READ".
//
// - Make links clickable.
//
// - Different text style for different types of messages (Me, Code, etc).
