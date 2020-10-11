// SPDX-License-Identifier: MIT

package chat

// Data structures to represent the types in sur/chat-store.hoon.
//
// Enumerations are handled by using pointers to the fields, which
// make them optional for the Go JSON package.

type Letter struct {
	Text *string `json:"text,omitempty"`
	URL  *string `json:"url,omitempty"`
	Code *struct {
		Expression string
		Output     [][]string
	} `json:"code,omitempty"`
	Me *string `json:"me,omitempty"`
}

type Envelope struct {
	UID    string  `json:"uid"`
	Number uint64  `json:"number"`
	Author string  `json:"author"`
	When   float64 `json:"when"`
	Letter Letter  `json:"letter"`
}

type Config struct {
	Length uint64
	Read   uint64
}

type Mailbox struct {
	Config    Config
	Envelopes []Envelope
}

type Inbox map[string]*Mailbox

type Message struct {
	Path     string   `json:"path"`
	Envelope Envelope `json:"envelope"`
}

type Messages struct {
	Path      string     `json:"path"`
	Envelopes []Envelope `json:"envelopes"`
}

type Update struct {
	Initial  *Inbox    `json:"initial"`
	Keys     *[]string `json:"keys"`
	Messages *Messages `json:"messages"`

	Create *struct {
		Path string `json:"path"`
	} `json:"create"`

	Delete *struct {
		Path string `json:"path"`
	} `json:"delete"`

	Message *Message `json:"message"`

	Read *struct {
		Path string `json:"path"`
	} `json:"read"`
}
