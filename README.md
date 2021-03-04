# Urbit HTTP API implementation in Go

[![PkgGoDev](https://pkg.go.dev/badge/github.com/cmarcelo/go-urbit)](https://pkg.go.dev/github.com/cmarcelo/go-urbit) [![awesome urbit badge](https://img.shields.io/badge/~-awesome%20urbit-lightgrey)](https://github.com/urbit/awesome-urbit)

This module provides functionality to communicate with Urbit
ships. See [Urbit home page](https://urbit.org) for more details about
it.

NOTE: the packages in this module are still in experimental stage, and
the API may change as it gets more developed -- the major version is
still 0.


## Basic usage

Connect with an Urbit ship using

```
ship, err := urbit.Dial(address, passcode, nil)
```

Note that the code can be obtained with the command `|code` in Dojo
inside the Urbit environment. The returned value is a Client that can
be used to send requests and receive events.

```
// Send a Poke
ship.Poke("chat-hook", jsonMessage)

// Subscribe to a path inside an app. The ID in result can be used to
// identify events containing updates for this subscription.
result, err := ship.Subscribe("chat-store", "/keys")

// ...

// Consume events.
for ev := range ship.Events() {
	switch (ev.Type) {
	case "diff":
		if (ev.ID == result.ID) {
			// ev.Data contains the data
		}
	}
}
```

It is important to consume the `Events` channel, because the Client
will not try to buffer individual events, so processing only happens
when that channel is consumed. Depending on the program, either a
separate goroutine or including the processing the in the program's
mainloop will be the typical solutions.

The next step after being able to talk, is to know what to talk. Most
apps running inside Urbit are able to talk via JSON. Their own
protocols are currently only documented in the source code (see `/sur`
in Urbit). It is also helpful to see how requests are created and
responses are parsed in Landscape (Urbit's reference web client).

For Go, these definitions can be mapped into types and serialization
helpers, see the [chat
package](https://github.com/cmarcelo/go-urbit/tree/main/chat) for
example.


## Examples

A good way to learn how to use the module is to look at the commented
[examples](https://github.com/cmarcelo/go-urbit/tree/main/examples). Run
each of them with

```
go run EXAMPLE.go
```

By default the examples will try to connect with the default address
and passcode used running local development ships ("fake zod"). They
can also be specified in the command line

```
go run EXAMPLE.go --addr ADDRESS --code CODE
```

each example has a few more options, see them with


```
go run EXAMPLE.go --help
```

