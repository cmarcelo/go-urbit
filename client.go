// SPDX-License-Identifier: MIT

package urbit

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Client struct {
	h *http.Client

	addr string
	code string

	trace bool

	channel string
	nextID  uint64

	name string

	events       chan *Event
	stream       io.Closer
	listenerDone chan struct{}

	ackerCh   chan []byte
	ackerDone chan struct{}

	mu            sync.Mutex
	pending       map[uint64]chan error
	subscriptions map[uint64]subscription

	closeOnce sync.Once
	close     chan struct{}
}

type subscription struct {
	ship, app, path string
}

func (c *Client) Name() string {
	return c.name
}

type DialOptions struct {
	Trace      bool
	HTTPClient *http.Client
}

func Dial(addr, code string, opts *DialOptions) (*Client, error) {
	c := &Client{
		addr: addr,
		code: code,

		listenerDone: make(chan struct{}),
		events:       make(chan *Event),

		ackerCh:   make(chan []byte),
		ackerDone: make(chan struct{}),

		pending:       make(map[uint64]chan error),
		subscriptions: make(map[uint64]subscription),

		close: make(chan struct{}, 1),
	}

	randomID := make([]byte, 6)
	_, err := rand.Read(randomID)
	if err != nil {
		return nil, fmt.Errorf("couldn't create random channel ID for ship: %w", err)
	}
	c.channel = fmt.Sprintf("/~/channel/%d-%s-go", time.Now().Unix(), hex.EncodeToString(randomID))

	if opts != nil {
		c.trace = opts.Trace
		c.h = opts.HTTPClient
	}

	if c.h == nil {
		cookieJar, _ := cookiejar.New(nil)
		c.h = &http.Client{Jar: cookieJar}
	}

	err = c.login()
	if err != nil {
		return nil, err
	}

	// TODO: Get this in a non-JS format so we don't need to tear
	// it apart.
	snippet, err := c.Get("/~landscape/js/session.js", "")
	if err != nil {
		return nil, fmt.Errorf("couldn't find ship name: %w", err)
	}
	parts := strings.SplitN(string(snippet), "'", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("couldn't find ship name in: %q", string(snippet))
	}
	c.name = parts[1]

	go c.listener()
	go c.acker()

	return c, nil
}

func (c *Client) login() error {
	data := url.Values{"password": {c.code}}
	resp, err := c.h.PostForm(c.addr+"/~/login", data)

	if err != nil {
		return fmt.Errorf("couldn't dial ship: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return fmt.Errorf("failed status when dialing ship: %s", resp.Status)
	}

	return nil
}

func (c *Client) openStream() (io.ReadCloser, error) {
	// Currently Eyre needs a new POST/PUT request to happen in an
	// (Eyre) channel before the client can GET to follow the
	// stream of responses.  This forces the client to delay
	// setting up the reader for stream of responses until the
	// first message is sent, so just go ahead and send that first
	// message.
	//
	// TODO: Make PR to Urbit for autocreation of the Eyre channel
	// on GET request.
	r := c.Hi(c.name, "hello from Go")
	if r.Err != nil {
		return nil, fmt.Errorf("couldn't say Hi to ship: %w", r.Err)
	}

	req, err := http.NewRequest(http.MethodGet, c.addr+c.channel, nil)
	if err != nil {
		return nil, fmt.Errorf("couldn't open a channel in ship: %w", err)
	}
	req.Header.Set("Content-Type", "text/event-stream")

	resp, err := c.h.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't open a channel in ship: %w", err)
	}

	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return nil, fmt.Errorf("failed status when opening a channel in ship: %s", resp.Status)
	}

	return resp.Body, nil
}

func (c *Client) Get(path, contentType string) ([]byte, error) {
	// TODO: Consider a streaming version that returns an
	// io.ReadCloser.

	if c.trace {
		fmt.Fprintf(os.Stderr, "GET: %s\n", path)
	}

	req, err := http.NewRequest(http.MethodGet, c.addr+path, nil)
	if err != nil {
		return nil, err
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.h.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't get %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return nil, fmt.Errorf("failed status when getting: %s", resp.Status)
	}

	return ioutil.ReadAll(resp.Body)
}

func (c *Client) GetJSON(path string) ([]byte, error) {
	return c.Get(path, "application/json")
}

func (c *Client) Scry(app, path string) ([]byte, error) {
	return c.GetJSON("/~/scry/" + app + path + ".json")
}

// TODO: Consider making this an interface and let user type-switch
// instead of switching on the Type string.
type Event struct {
	ID   uint64
	Type string `json:"response"`

	Ok  *string
	Err *string

	Data json.RawMessage `json:"json"`
}

func (c *Client) Events() <-chan *Event {
	return c.events
}

const (
	ClientError = "go-client-error"
)

func (c *Client) dispatchError(e error, data []byte) {
	msg := e.Error()
	c.events <- &Event{
		Type: ClientError,
		Err:  &msg,
		Data: data,
	}
}

func truncate(s string, size int) string {
	if len(s) < size+3 {
		return s
	}
	return fmt.Sprintf("%s...", s[:size])
}

func (c *Client) listener() {
loop:
	for {
		select {
		case <-c.close:
			break loop
		default:
			// Continue.
		}

		var sse *sseReader

		stream, err := c.openStream()
		if err != nil {
			// TODO: Identify the case where re-login is needed.
			c.dispatchError(fmt.Errorf("failed to open stream: %w\n", err), nil)
			goto retry
		}

		sse = newSSEReader(stream)
		c.stream = stream

		for sse.Next() {
			c.processEvent(sse)
		}

		select {
		case <-c.close:
			break loop
		default:
			// Continue.
		}

	retry:
		// TODO: Implement a limit and back-off strategy to retries.

		// TODO: Could we just notify the client via an Event
		// and let it control whether/when to retry?

		if c.trace {
			fmt.Fprintf(os.Stderr, "INFO: waiting to retry opening stream\n")
		}

		retryTimer := time.NewTimer(5 * time.Second)
		select {
		case <-c.close:
			break loop
		case <-retryTimer.C:
			// Continue.
		}
	}

	if c.trace {
		fmt.Fprintf(os.Stderr, "CLOSING: listener shutting down\n")
	}

	close(c.events)

	c.listenerDone <- struct{}{}
}

func (c *Client) processEvent(sse *sseReader) {
	// sseReader will reuse internal buffers, so when dispatching
	// errors, copy the buffer contents so that it doesn't get
	// overriden.  This isn't an issue for the successful code
	// path because the result of unmarshalling won't reuse that
	// buffer.

	var ev Event
	err := json.Unmarshal(sse.Data, &ev)
	if err != nil {
		c.dispatchError(err, append([]byte(nil), sse.Data...))
		return
	}

	if c.trace {
		var okStatus string
		if ev.Ok != nil {
			okStatus = fmt.Sprintf(" ok=%q", *ev.Ok)
		}
		var errStatus string
		if ev.Err != nil {
			errStatus = fmt.Sprintf(" err=%q", *ev.Err)
		}
		data := truncate(string(ev.Data), 256)
		fmt.Fprintf(os.Stderr, "RECV: %d <%s> %s%s%s data=%s (%d bytes)\n", ev.ID, string(sse.LastEventID), ev.Type, okStatus, errStatus, data, len(ev.Data))
	}

	// NOTE: Eyre generates Server-Sent Events, but it doesn't use
	// "event" or "retry" fields.

	switch ev.Type {
	case "poke", "subscribe":
		c.mu.Lock()
		ch, exists := c.pending[ev.ID]
		if exists {
			delete(c.pending, ev.ID)
		}
		c.mu.Unlock()

		if !exists {
			c.dispatchError(fmt.Errorf("response for unknown request with action=%s id=%s", ev.Type, string(sse.LastEventID)), append([]byte(nil), sse.Data...))
			return
		}

		var responseErr error
		if ev.Ok != nil {
			responseErr = nil
		} else if ev.Err != nil {
			responseErr = fmt.Errorf("response=%s id=%d failed: %s", ev.Type, ev.ID, *ev.Err)
		} else {
			responseErr = fmt.Errorf("response=%s id=%d had invalid response", ev.Type, ev.ID)
		}
		ch <- responseErr

	case "diff":
		// No book-keeping needed, will just pass to the channel.

	case "quit":
		// TODO: Retry subscribe if this was unexpected.

	default:
		c.dispatchError(fmt.Errorf("unknown response=%s for id=%d", ev.Type, ev.ID), append([]byte(nil), sse.Data...))
		return
	}

	c.events <- &ev
	c.ackerCh <- sse.LastEventID
}

const (
	ActionPoke        = "poke"
	ActionSubscribe   = "subscribe"
	ActionUnsubscribe = "unsubscribe"
)

// TODO: For now this is essentially an union of the valid request
// fields. Should we make this an interface?
type Request struct {
	Action string `json:"action"`

	// Poke and Subscribe only.
	Ship string `json:"ship,omitempty"`
	App  string `json:"app,omitempty"`

	// Subscribe only.
	Path string `json:"path,omitempty"`

	// Poke only.
	Mark string          `json:"mark,omitempty"`
	Data json.RawMessage `json:"json,omitempty"`

	// Unsubscribe only.
	Subscription uint64 `json:"subscription,omitempty"`
}

// TODO: Should we allow a private id to be assigned (so we can have
// it before blocking)?

type Result struct {
	ID       uint64
	Err      error
	Response <-chan error
}

// TODO: Add comment about the need for the client process events in a
// different goroutine when using Wait.
func (res *Result) Wait() error {
	if res.Response != nil {
		res.Err = <-res.Response
		res.Response = nil
	}
	return res.Err
}

func (c *Client) Poke(app string, data json.RawMessage) Result {
	req := &Request{
		Action: ActionPoke,
		Ship:   c.name,
		App:    app,
		Mark:   "json",
		Data:   data,
	}
	return c.Do(req)
}

func (c *Client) PokeShip(ship, app string, data json.RawMessage) Result {
	req := &Request{
		Action: ActionPoke,
		Ship:   ship,
		App:    app,
		Mark:   "json",
		Data:   data,
	}
	return c.Do(req)
}

func (c *Client) getNextID() uint64 {
	// TODO: Comment on the usefulness of ids starting with non-zero.
	return atomic.AddUint64(&c.nextID, 1)
}

func (c *Client) Do(req *Request) Result {
	return c.DoMany([]*Request{req})[0]
}

func (c *Client) DoMany(reqs []*Request) []Result {
	results := make([]Result, len(reqs))
	lastID := atomic.AddUint64(&c.nextID, uint64(len(reqs)))

	type requestWithID struct {
		*Request
		ID uint64     `json:"id"`
		Ch chan error `json:"-"`
	}

	reqsWithID := make([]requestWithID, len(reqs))

	for i, req := range reqs {
		id := lastID - uint64(i)
		hasResponse := req.Action == ActionPoke || req.Action == ActionSubscribe
		var ch chan error
		if hasResponse {
			// This channel will only have a single value
			// sent to it: either nil or the error after
			// the message was sent.  To make optional for
			// the client to consume it (via Wait()
			// function), add a 1-size buffer to it.
			ch = make(chan error, 1)

			c.mu.Lock()
			c.pending[id] = ch
			c.mu.Unlock()
		}

		switch req.Action {
		case ActionSubscribe:
			c.mu.Lock()
			c.subscriptions[id] = subscription{req.Ship, req.App, req.Path}
			c.mu.Unlock()
		case ActionUnsubscribe:
			c.mu.Lock()
			delete(c.subscriptions, id)
			c.mu.Unlock()
		}

		reqsWithID[i] = requestWithID{req, id, ch}
	}

	data, err := json.Marshal(reqsWithID)
	if err != nil {
		c.mu.Lock()
		for i, req := range reqsWithID {
			delete(c.pending, req.ID)
			results[i].Err = err
		}
		c.mu.Unlock()
		return results
	}

	if c.trace {
		for _, req := range reqsWithID {
			var details string
			switch req.Action {
			case ActionPoke:
				data := truncate(string(req.Data), 256)
				details = fmt.Sprintf("~%s app=%s mark=%s data=%s (%d bytes)", req.Ship, req.App, req.Mark, data, len(req.Data))
			case ActionSubscribe:
				details = fmt.Sprintf("~%s app=%s path=%s", req.Ship, req.App, req.Path)
			case ActionUnsubscribe:
				details = fmt.Sprintf("subscription=%d", req.Subscription)
			}

			fmt.Fprintf(os.Stderr, "SEND: %d %s %s\n", req.ID, req.Action, details)
		}
	}

	buf := bytes.NewBuffer(data)
	err = c.putJSON(buf)
	if err != nil {
		c.mu.Lock()
		for i, req := range reqsWithID {
			delete(c.pending, req.ID)
			results[i].Err = err
		}
		c.mu.Unlock()
		return results
	}

	for i, req := range reqsWithID {
		results[i].ID = req.ID
		results[i].Response = req.Ch
	}

	return results
}

// acker runs in a goroutine and coalesces the ACK messages so that
// when Client receives a series of messages in a short amount of
// time, only a single ACK for the latest ID is sent.
func (c *Client) acker() {
	var lastAckID []byte
	var pendingAckID []byte

	var timerCh <-chan time.Time
	timer := time.NewTimer(time.Hour)
	timer.Stop()

loop:
	for {
		select {
		case id := <-c.ackerCh:
			if id == nil {
				break loop
			}
			pendingAckID = id
			if timerCh == nil {
				timerCh = timer.C
				timer.Reset(time.Second)
			}

		case <-timer.C:
			if !bytes.Equal(lastAckID, pendingAckID) {
				c.ack(pendingAckID)
				lastAckID = pendingAckID
			}
			timerCh = nil
		}
	}

	if timerCh != nil {
		timer.Stop()
	}

	c.ackerDone <- struct{}{}

	if c.trace {
		fmt.Fprintf(os.Stderr, "CLOSING: acker shutting down\n")
	}
}

func (c *Client) ack(eventID []byte) {
	if c.trace {
		fmt.Fprintf(os.Stderr, "ACK: <%s>\n", string(eventID))
	}
	buf := &bytes.Buffer{}
	buf.Write([]byte(`[{"action": "ack", "event-id": `))
	buf.Write(eventID)
	buf.Write([]byte(` }]`))

	err := c.putJSON(buf)
	if err != nil {
		c.dispatchError(fmt.Errorf("failure to send ack for event-id=%s: %w", string(eventID), err), nil)
		return
	}
}

// TODO: Should ACK be controlled by the user?

func (c *Client) putJSON(body io.Reader) error {
	req, err := http.NewRequest(http.MethodPut, c.addr+c.channel, body)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.h.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return fmt.Errorf("failed status when calling put: %s", resp.Status)
	}

	return nil
}

func (c *Client) Hi(ship, message string) Result {
	req := &Request{
		Action: ActionPoke,
		Ship:   ship,
		App:    "hood",
		Mark:   "helm-hi",
		Data:   json.RawMessage(strconv.Quote(message)),
	}
	return c.Do(req)
}

func (c *Client) Subscribe(app, path string) Result {
	req := &Request{
		Action: ActionSubscribe,
		Ship:   c.name,
		App:    app,
		Path:   path,
	}
	return c.Do(req)
}

func (c *Client) SubscribeShip(ship, app, path string) Result {
	req := &Request{
		Action: ActionSubscribe,
		Ship:   ship,
		App:    app,
		Path:   path,
	}
	return c.Do(req)
}

func (c *Client) Unsubscribe(subscriptionID uint64) Result {
	req := &Request{
		Action:       ActionUnsubscribe,
		Subscription: subscriptionID,
	}
	return c.Do(req)
}

func (c *Client) Close() error {
	var err error

	c.closeOnce.Do(func() {
		if c.trace {
			fmt.Fprintf(os.Stderr, "CLOSING: ~%s\n", c.Name())
		}

		// Mark that the user asked to Close. This is a value
		// in a channel and not a boolean so that if listener
		// goroutine is waiting for a retry, it can also catch
		// it.
		c.close <- struct{}{}

		// Close the current stream. This will make the SSE
		// Reader loop stop.
		if c.stream != nil {
			// TODO: Possible race condition here.
			c.stream.Close()
		}

		// Consume any pending events. If listener is stuck
		// trying to send an event, this will make sure it
		// make progress and catch up with the closing
		// procedure.
		for range c.events {
			// Nothing.
		}

		// Wait for listener to finish.
		<-c.listenerDone

		// Tell acker goroutine to shutdown.
		c.ackerCh <- nil
		<-c.ackerDone

		err = c.postForm("/~/logout", nil)
		if err != nil {
			return
		}

		if c.trace {
			if err != nil {
				fmt.Fprintf(os.Stderr, "CLOSED: ~%s error: %s\n", c.Name(), err)
			} else {
				fmt.Fprintf(os.Stderr, "CLOSED: ~%s\n", c.Name())
			}
		}

		// TODO: Set some state so that Do and friends also stop working?
	})

	return err
}

func (c *Client) postForm(path string, data url.Values) error {
	if c.trace {
		fmt.Fprintf(os.Stderr, "POST: %s\n", path)
	}

	resp, err := c.h.PostForm(c.addr+path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		return fmt.Errorf("failed status when POST: %s", resp.Status)
	}

	return nil
}
