package cloudflare

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"

	"github.com/gatework/cloudflare-go/internal/dns"
)

// Selected records share their retained raw bytes with the final result. Only
// filtered records need a fingerprint; their large bodies can then be collected.
type recordSnapshot struct {
	raw    json.RawMessage
	digest [sha256.Size]byte
}

func snapshot(raw json.RawMessage, selected bool) (recordSnapshot, error) {
	if selected {
		return recordSnapshot{raw: raw}, nil
	}
	digest, err := jsonDigest(raw)
	return recordSnapshot{digest: digest}, err
}

func (s recordSnapshot) matches(raw json.RawMessage) (bool, error) {
	if s.raw != nil {
		return dns.EqualJSON(s.raw, raw), nil
	}
	digest, err := jsonDigest(raw)
	return err == nil && digest == s.digest, err
}

func jsonDigest(raw []byte) ([sha256.Size]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return [sha256.Size]byte{}, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return [sha256.Size]byte{}, errors.New("JSON 包含尾随数据")
	}
	// encoding/json sorts map keys and preserves json.Number's original precision.
	canonical, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}
