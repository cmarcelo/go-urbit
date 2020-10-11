// SPDX-License-Identifier: MIT

package urbit

import (
	"bytes"
	"testing"
	"time"
)

func TestSSE(t *testing.T) {
	type event struct {
		Data  string
		Type  string
		ID    string
		Retry time.Duration
	}

	tests := []struct {
		Name     string
		Input    string
		Expected []event
	}{
		{
			Name: "spec first example",
			Input: `
data: This is the first message.

data: This is the second message, it
data: has two lines.

data: This is the third message.

`,
			Expected: []event{
				{Data: "This is the first message."},
				{Data: "This is the second message, it\nhas two lines."},
				{Data: "This is the third message."},
			},
		},

		{
			Name: "spec two event types example",
			Input: `
event: add
data: 73857293

event: remove
data: 2153

event: add
data: 113411

`,
			Expected: []event{
				{Type: "add", Data: "73857293"},
				{Type: "remove", Data: "2153"},
				{Type: "add", Data: "113411"},
			},
		},

		{
			Name: "spec stock example",
			Input: `
data: YHOO
data: +2
data: 10

`,
			Expected: []event{{Data: "YHOO\n+2\n10"}},
		},

		{
			Name: "spec example with missing empty line at the end",
			Input: `
: test stream

data: first event
id: 1

data:second event
id

data:  third event
`,
			Expected: []event{
				{Data: "first event", ID: "1"},
				{Data: "second event", ID: ""},
			},
		},

		{
			Name: "spec example with empty data and missing empty line at the end",
			Input: `
data

data
data

data:
`,
			Expected: []event{
				{Data: ""},
				{Data: "\n"},
			},
		},

		{
			Name: "spec example where space after colon is ignored",
			Input: `
data:test

data: test

`,
			Expected: []event{{Data: "test"}, {Data: "test"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			sse := newSSEReader(bytes.NewBufferString(tt.Input))

			for _, e := range tt.Expected {
				if !sse.Next() {
					if sse.Err != nil {
						t.Errorf("in test %q expected %q but got error: %s", tt.Name, e.Data, sse.Err)
					} else {
						t.Errorf("in test %q expected %q but got end of file", tt.Name, e.Data)
					}
				}

				gotType := string(sse.Type)
				if gotType != e.Type {
					t.Errorf("in test %q type mismatch: expected %q but got %q", tt.Name, e.Type, gotType)
				}

				gotData := string(sse.Data)
				if gotData != e.Data {
					t.Errorf("in test %q data mismatach: expected %q but got %q", tt.Name, e.Data, gotData)
				}

				gotID := string(sse.LastEventID)
				if gotID != e.ID {
					t.Errorf("in test %q last event ID mismatach: expected %q but got %q", tt.Name, e.ID, gotID)
				}

				gotRetry := sse.ReconnectionTime
				if gotRetry != e.Retry {
					t.Errorf("in test %q reconnection time mismatach: expected %s but got %s", tt.Name, e.Retry, gotRetry)
				}
			}

			if sse.Next() {
				t.Errorf("in test %q expected end of file but got data %q", tt.Name, string(sse.Data))
			}

			if sse.Err != nil {
				t.Errorf("in test %q unexpected error at the end: %s", tt.Name, sse.Err)
			}
		})
	}
}
