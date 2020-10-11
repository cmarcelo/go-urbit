// SPDX-License-Identifier: MIT

package chat

import (
	"bytes"
	"math/rand"
	"strconv"
)

// GenerateUID generates an unique identifer suitable to be used by a
// chat Envelope.  It uses the default math/rand generator so its up
// to the caller to seed it with rand.Seed().
//
// TODO: Take some parameter from rand package instead of relying on
// the global generator.  Or have both options.
func GenerateUID() string {
	buf := bytes.Buffer{}
	buf.WriteString("0v")
	buf.WriteString(strconv.FormatUint(uint64(rand.Intn(7)+1), 10))
	for i := 0; i < 5; i++ {
		buf.WriteByte('.')
		s := strconv.FormatUint(uint64(rand.Intn(1<<25)), 32)
		for j := len(s); j < 5; j++ {
			buf.WriteByte('0')
		}
		buf.WriteString(s)
	}
	return buf.String()
}
