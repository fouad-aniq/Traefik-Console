package auth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
)

var errTokenSignature = errors.New("the ID token signature is invalid")

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwsHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// parseJWS splits a compact JWS and decodes its header and payload. It does
// not verify anything.
func parseJWS(token string) (header jwsHeader, payload []byte, signingInput, signature []byte, err error) {
	if len(token) > 32*1024 {
		return header, nil, nil, nil, errors.New("the ID token is too large")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return header, nil, nil, nil, errors.New("the ID token is malformed")
	}
	rawHeader, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(rawHeader, &header) != nil {
		return header, nil, nil, nil, errors.New("the ID token header is malformed")
	}
	if payload, err = base64.RawURLEncoding.DecodeString(parts[1]); err != nil {
		return header, nil, nil, nil, errors.New("the ID token payload is malformed")
	}
	if signature, err = base64.RawURLEncoding.DecodeString(parts[2]); err != nil {
		return header, nil, nil, nil, errors.New("the ID token signature is malformed")
	}
	return header, payload, []byte(parts[0] + "." + parts[1]), signature, nil
}

// candidateKeys returns the keys that may have signed a token with this header.
func (s *jwkSet) candidateKeys(h jwsHeader) []jwk {
	var out []jwk
	for _, k := range s.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if h.Kid != "" && k.Kid != h.Kid {
			continue
		}
		if k.Alg != "" && k.Alg != h.Alg {
			continue
		}
		out = append(out, k)
	}
	return out
}

// verifySignature checks the JWS signature with one key. Only asymmetric
// algorithms are accepted; "none" and HMAC are always rejected.
func verifySignature(alg string, key jwk, signingInput, sig []byte) error {
	hashFor := func(h crypto.Hash) []byte {
		hh := h.New()
		hh.Write(signingInput)
		return hh.Sum(nil)
	}
	switch alg {
	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512":
		pub, err := key.rsaKey()
		if err != nil {
			return err
		}
		h := map[byte]crypto.Hash{'2': crypto.SHA256, '3': crypto.SHA384, '5': crypto.SHA512}[alg[2]]
		if alg[0] == 'R' {
			return rsa.VerifyPKCS1v15(pub, h, hashFor(h), sig)
		}
		return rsa.VerifyPSS(pub, h, hashFor(h), sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	case "ES256", "ES384", "ES512":
		pub, err := key.ecKey()
		if err != nil {
			return err
		}
		var digest []byte
		var size int
		switch alg {
		case "ES256":
			digest, size = sha256Sum(signingInput), 32
		case "ES384":
			d := sha512.Sum384(signingInput)
			digest, size = d[:], 48
		default:
			d := sha512.Sum512(signingInput)
			digest, size = d[:], 66
		}
		if pub.Curve.Params().BitSize != map[string]int{"ES256": 256, "ES384": 384, "ES512": 521}[alg] || len(sig) != 2*size {
			return errTokenSignature
		}
		r, sv := new(big.Int).SetBytes(sig[:size]), new(big.Int).SetBytes(sig[size:])
		if !ecdsa.Verify(pub, digest, r, sv) {
			return errTokenSignature
		}
		return nil
	case "EdDSA":
		if key.Kty != "OKP" || key.Crv != "Ed25519" {
			return errTokenSignature
		}
		x, err := base64.RawURLEncoding.DecodeString(key.X)
		if err != nil || len(x) != ed25519.PublicKeySize {
			return errTokenSignature
		}
		if !ed25519.Verify(ed25519.PublicKey(x), signingInput, sig) {
			return errTokenSignature
		}
		return nil
	default:
		return errors.New("the ID token uses an unsupported signing algorithm")
	}
}

func sha256Sum(b []byte) []byte {
	d := sha256.Sum256(b)
	return d[:]
}

func (k jwk) rsaKey() (*rsa.PublicKey, error) {
	if k.Kty != "RSA" {
		return nil, errTokenSignature
	}
	n, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil || len(n) < 256 {
		return nil, errors.New("the provider RSA key is invalid or shorter than 2048 bits")
	}
	e, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil || len(e) == 0 || len(e) > 4 {
		return nil, errTokenSignature
	}
	exp := 0
	for _, b := range e {
		exp = exp<<8 | int(b)
	}
	if exp < 3 {
		return nil, errTokenSignature
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}, nil
}

func (k jwk) ecKey() (*ecdsa.PublicKey, error) {
	if k.Kty != "EC" {
		return nil, errTokenSignature
	}
	var curve elliptic.Curve
	switch k.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, errTokenSignature
	}
	x, err1 := base64.RawURLEncoding.DecodeString(k.X)
	y, err2 := base64.RawURLEncoding.DecodeString(k.Y)
	if err1 != nil || err2 != nil {
		return nil, errTokenSignature
	}
	byteLen := (curve.Params().BitSize + 7) / 8
	if len(x) != byteLen || len(y) != byteLen {
		return nil, errTokenSignature
	}
	// Build the uncompressed point and let crypto/ecdh-backed parsing reject off-curve points.
	point := append([]byte{4}, append(x, y...)...)
	pub, err := ecdsa.ParseUncompressedPublicKey(curve, point)
	if err != nil {
		return nil, errTokenSignature
	}
	return pub, nil
}
