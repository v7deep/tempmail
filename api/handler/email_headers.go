package handler

import "mime"

// Decode headers on read as well so messages stored by older receivers remain
// readable without changing their stored fields or original MIME message.
func decodeEmailHeader(value string) string {
	decoded, err := new(mime.WordDecoder).DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}
