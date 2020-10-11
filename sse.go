// SPDX-License-Identifier: MIT

package urbit

import (
	"bufio"
	"bytes"
	"io"
	"math"
	"strconv"
	"time"
)

// Process Server-Sent Events as defined in https://www.w3.org/TR/eventsource/.

type sseReader struct {
	s           *bufio.Scanner
	dataBuffer  bytes.Buffer
	lastEventID []byte

	Type        []byte
	Data        []byte
	LastEventID []byte

	Err              error
	ReconnectionTime time.Duration
}

func newSSEReader(r io.Reader) *sseReader {
	sr := &sseReader{
		s: bufio.NewScanner(r),
	}

	// TODO: Take this as parameter? Or make a separate constructor?
	sr.s.Buffer([]byte(nil), 1024*1024)

	return sr
}

func (sr *sseReader) Next() bool {
	var eventType []byte
	sr.dataBuffer.Reset()

	for sr.s.Scan() {
		line := sr.s.Bytes()

		if len(line) == 0 {
			if sr.dataBuffer.Len() > 0 {
				var data []byte
				data = sr.dataBuffer.Bytes()
				if data[len(data)-1] == '\n' {
					data = data[:len(data)-1]
				}
				sr.Type = eventType
				sr.Data = data
				sr.LastEventID = sr.lastEventID
				return true
			}

			sr.dataBuffer.Reset()
			continue
		}

		if line[0] == ':' {
			continue
		}

		var field []byte
		var value []byte

		colon := bytes.Index(line, []byte(":"))
		if colon == -1 {
			field = line
		} else {
			field = line[:colon]
			value = line[colon+1:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
		}

		switch {
		case bytes.Equal(field, []byte("event")):
			eventType = append([]byte(nil), value...)
		case bytes.Equal(field, []byte("data")):
			sr.dataBuffer.Write(value)
			sr.dataBuffer.WriteByte('\n')
		case bytes.Equal(field, []byte("id")):
			sr.lastEventID = append([]byte(nil), value...)
		case bytes.Equal(field, []byte("retry")):
			value = bytes.TrimSpace(value)
			if v, err := strconv.ParseUint(string(value), 10, 64); err != nil {
				const maxMiliseconds = uint64(math.MaxInt64 / time.Millisecond)
				if v > maxMiliseconds {
					v = maxMiliseconds
				}
				sr.ReconnectionTime = time.Duration(v) * time.Millisecond
			}
		default:
			continue
		}
	}

	sr.Type = nil
	sr.Data = nil
	sr.Err = sr.s.Err()
	return false
}
