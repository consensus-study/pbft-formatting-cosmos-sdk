// Package crypto provides cryptographic utilities for the PBFT consensus engine.
package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
)

// KeyPair represents an ECDSA key pair.
type KeyPair struct {
	PrivateKey *ecdsa.PrivateKey // ECDSA P-256 개인키
	PublicKey  *ecdsa.PublicKey // 공개키
}

// Signature represents a digital signature.
type Signature struct {
	R *big.Int
	S *big.Int
}

// GenerateKeyPair generates a new ECDSA key pair using P-256 curve.
func GenerateKeyPair() (*KeyPair, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key pair: %w", err)
	}

	return &KeyPair{
		PrivateKey: privateKey,
		PublicKey:  &privateKey.PublicKey,
	}, nil
}

// Sign signs a message using the private key.
func (kp *KeyPair) Sign(message []byte) (*Signature, error) {
	hash := sha256.Sum256(message)
	r, s, err := ecdsa.Sign(rand.Reader, kp.PrivateKey, hash[:])
	if err != nil {
		return nil, fmt.Errorf("failed to sign message: %w", err)
	}

	return &Signature{R: r, S: s}, nil
}

// Verify verifies a signature against a message and public key.
func Verify(publicKey *ecdsa.PublicKey, message []byte, sig *Signature) bool {
	hash := sha256.Sum256(message)
	return ecdsa.Verify(publicKey, hash[:], sig.R, sig.S)
}

// PublicKeyBytes returns the public key as bytes.
func (kp *KeyPair) PublicKeyBytes() []byte {
	return elliptic.Marshal(kp.PublicKey.Curve, kp.PublicKey.X, kp.PublicKey.Y)
}

// PublicKeyFromBytes reconstructs a public key from bytes.
func PublicKeyFromBytes(data []byte) (*ecdsa.PublicKey, error) {
	x, y := elliptic.Unmarshal(elliptic.P256(), data)
	if x == nil {
		return nil, fmt.Errorf("invalid public key bytes")
	}

	return &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     x,
		Y:     y,
	}, nil
}

// SignatureBytes returns the signature as bytes.
func (s *Signature) Bytes() []byte {
	rBytes := s.R.Bytes()
	sBytes := s.S.Bytes()

	// Pad to 32 bytes each
	signature := make([]byte, 64)
	copy(signature[32-len(rBytes):32], rBytes)
	copy(signature[64-len(sBytes):], sBytes)

	return signature
}

// SignatureFromBytes reconstructs a signature from bytes.
func SignatureFromBytes(data []byte) (*Signature, error) {
	if len(data) != 64 {
		return nil, fmt.Errorf("invalid signature length: expected 64, got %d", len(data))
	}

	r := new(big.Int).SetBytes(data[:32])
	s := new(big.Int).SetBytes(data[32:])

	return &Signature{R: r, S: s}, nil
}

// Hash computes SHA256 hash of data.
func Hash(data []byte) []byte {
	hash := sha256.Sum256(data)
	return hash[:]
}

// HashHex computes SHA256 hash and returns as hex string.
func HashHex(data []byte) string {
	return hex.EncodeToString(Hash(data))
}

// DoubleHash computes double SHA256 hash (SHA256(SHA256(data))).
func DoubleHash(data []byte) []byte {
	first := sha256.Sum256(data)
	second := sha256.Sum256(first[:])
	return second[:]
}

// MerkleRoot computes the Merkle root of a list of hashes.
func MerkleRoot(hashes [][]byte) []byte {
	if len(hashes) == 0 {
		return nil
	}

	if len(hashes) == 1 {
		return hashes[0]
	}

	// Make a copy to avoid modifying the original
	level := make([][]byte, len(hashes))
	copy(level, hashes)

	for len(level) > 1 {
		// If odd number, duplicate the last hash
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}

		nextLevel := make([][]byte, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			combined := append(level[i], level[i+1]...)
			nextLevel[i/2] = Hash(combined)
		}
		level = nextLevel
	}

	return level[0]
}

// ValidatorID generates a validator ID from a public key.
func ValidatorID(publicKey []byte) string {
	hash := Hash(publicKey)
	return hex.EncodeToString(hash[:20]) // Use first 20 bytes
}

// RandomBytes generates random bytes of the specified length.
func RandomBytes(length int) ([]byte, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return nil, fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return bytes, nil
}

// Signer interface for signing operations.
type Signer interface {
	Sign(message []byte) ([]byte, error)
	PublicKey() []byte
	Address() string
}

// DefaultSigner implements the Signer interface using ECDSA.
type DefaultSigner struct {
	keyPair *KeyPair
	address string
}

// NewDefaultSigner creates a new DefaultSigner with a generated key pair.
func NewDefaultSigner() (*DefaultSigner, error) {
	kp, err := GenerateKeyPair()
	if err != nil {
		return nil, err
	}

	return &DefaultSigner{
		keyPair: kp,
		address: ValidatorID(kp.PublicKeyBytes()),
	}, nil
}

// NewDefaultSignerFromKeyPair creates a DefaultSigner from an existing key pair.
func NewDefaultSignerFromKeyPair(kp *KeyPair) *DefaultSigner {
	return &DefaultSigner{
		keyPair: kp,
		address: ValidatorID(kp.PublicKeyBytes()),
	}
}

// Sign signs a message.
func (s *DefaultSigner) Sign(message []byte) ([]byte, error) {
	sig, err := s.keyPair.Sign(message)
	if err != nil {
		return nil, err
	}
	return sig.Bytes(), nil
}

// PublicKey returns the public key bytes.
func (s *DefaultSigner) PublicKey() []byte {
	return s.keyPair.PublicKeyBytes()
}

// Address returns the signer's address.
func (s *DefaultSigner) Address() string {
	return s.address
}

// VerifyWithPublicKey verifies a signature with a public key bytes.
func VerifyWithPublicKey(publicKeyBytes, message, signatureBytes []byte) (bool, error) {
	publicKey, err := PublicKeyFromBytes(publicKeyBytes)
	if err != nil {
		return false, err
	}

	sig, err := SignatureFromBytes(signatureBytes)
	if err != nil {
		return false, err
	}

	return Verify(publicKey, message, sig), nil
}
