package kms

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

// AlgorithmAES256GCM denotes AES-256 in Galois/Counter Mode.
const AlgorithmAES256GCM = "AES-256-GCM"

// ErrInvalidEnvelope is returned when decoding an incomplete or malformed envelope.
var ErrInvalidEnvelope = errors.New("kms: invalid envelope structure")

// Envelope encapsulates the ciphertext along with its encrypted data key (DEK) and cryptographic parameters.
type Envelope struct {
	KeyID        string `json:"key_id"`
	EncryptedKey []byte `json:"encrypted_key"`
	Ciphertext   []byte `json:"ciphertext"`
	Nonce        []byte `json:"nonce"`
	Algorithm    string `json:"algorithm"`
}

// Marshal encodes the Envelope as JSON.
func (e *Envelope) Marshal() ([]byte, error) {
	if e == nil {
		return nil, ErrInvalidEnvelope
	}
	return json.Marshal(e)
}

// Unmarshal decodes JSON into the Envelope.
func (e *Envelope) Unmarshal(data []byte) error {
	if err := json.Unmarshal(data, e); err != nil {
		return err
	}
	if len(e.EncryptedKey) == 0 || len(e.Ciphertext) == 0 || len(e.Nonce) == 0 {
		return ErrInvalidEnvelope
	}
	return nil
}

// MarshalBase64 encodes the Envelope as a URL-safe base64 string.
func (e *Envelope) MarshalBase64() (string, error) {
	data, err := e.Marshal()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// UnmarshalBase64 decodes a URL-safe base64 string into the Envelope.
func (e *Envelope) UnmarshalBase64(s string) error {
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	return e.Unmarshal(data)
}
