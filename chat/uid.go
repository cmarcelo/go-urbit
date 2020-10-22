// SPDX-License-Identifier: MIT

package chat

import (
	"bytes"
	"encoding/base32"
	"math/rand"
)

// GenerateUID generates an unique identifer suitable to be used by a
// chat Envelope.  The UID is 128-bit number represented in base32,
// with a dot character separating each 5 digits, prefixed with 0v.
// For example: "0v5.acis6.788ro.bljvu.prn7k.22d4q".
func GenerateUID() (string, error) {
	b := make([]byte, 20)
	_, err := rand.Read(b[4:])
	if err != nil {
		return "", err
	}

	uid := make([]byte, 32)
	base32.HexEncoding.Encode(uid, b)
	uid = bytes.ToLower(uid)
	uid = uid[6:]

	buf := bytes.Buffer{}
	buf.Write([]byte("0v"))
	buf.WriteByte(uid[0])
	uid = uid[1:]
	for i := 0; i < 5; i++ {
		buf.WriteByte('.')
		buf.Write(uid[:5])
		uid = uid[5:]
	}

	return buf.String(), nil
}
