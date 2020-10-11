// SPDX-License-Identifier: MIT

package chat

import "encoding/json"

// parseUpdate will parse a JSON byte array of a chat-update
// message into a chat.Update type.
func ParseUpdate(data []byte) (*Update, error) {
	var j struct {
		Update Update `json:"chat-update"`
	}

	err := json.Unmarshal(data, &j)
	if err != nil {
		return nil, err
	}

	return &j.Update, nil
}
