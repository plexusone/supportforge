package supportforge

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
)

// referenceAlphabet is Crockford's base32 without I, L, O and U, so a
// reference read aloud or retyped is not misread.
const referenceAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

type idSource struct {
	mu  sync.Mutex
	src io.Reader
}

func newIDSource(src io.Reader) *idSource {
	if src == nil {
		src = rand.Reader
	}
	return &idSource{src: src}
}

func (s *idSource) read(n int) ([]byte, error) {
	b := make([]byte, n)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := io.ReadFull(s.src, b); err != nil {
		return nil, fmt.Errorf("supportforge: random source: %w", err)
	}
	return b, nil
}

// id returns a 128-bit random identifier in hex.
func (s *idSource) id() (string, error) {
	b, err := s.read(16)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// reference returns a short reference such as SR-7K2M9Q: 30 random bits,
// unique enough with the store's duplicate check and a retry.
func (s *idSource) reference() (string, error) {
	b, err := s.read(6)
	if err != nil {
		return "", err
	}
	out := []byte("SR-")
	for _, c := range b {
		out = append(out, referenceAlphabet[int(c)%len(referenceAlphabet)])
	}
	return string(out), nil
}
