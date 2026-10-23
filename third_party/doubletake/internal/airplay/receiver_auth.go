package airplay

import (
	"bytes"
	"crypto/ed25519"
	"errors"

	"golang.org/x/crypto/chacha20poly1305"
)

var ErrReceiverAuthentication = errors.New("receiver identity authentication failed")

// Aircast opts into mutual authentication. The upstream CLI retains its
// compatibility policy; the engine never treats decryption alone as proof of
// the receiver's long-term identity.
func (c *AirPlayClient) RequireReceiverAuthentication() {
	c.requireReceiverAuthentication = true
}

func (c *AirPlayClient) ReceiverPairingIdentity() (string, []byte) {
	return c.receiverPairingID, append([]byte(nil), c.receiverPublicKey...)
}

func (c *AirPlayClient) authenticateReceiverSetup(encrypted, sessionKey, sharedKey []byte) error {
	aead, err := chacha20poly1305.New(sessionKey)
	if err != nil {
		return ErrReceiverAuthentication
	}
	var nonce [12]byte
	copy(nonce[4:], "PS-Msg06")
	plain, err := aead.Open(nil, nonce[:], encrypted, nil)
	if err != nil {
		return ErrReceiverAuthentication
	}
	identity := tlv8Decode(plain)
	id, publicKey, signature := identity[tlvIdentifier], identity[tlvPublicKey], identity[tlvSignature]
	if len(id) == 0 || len(id) > 1024 || len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize {
		return ErrReceiverAuthentication
	}
	signKey := hkdfSHA512(sharedKey, []byte("Pair-Setup-Accessory-Sign-Salt"), []byte("Pair-Setup-Accessory-Sign-Info"), 32)
	signed := bytes.Join([][]byte{signKey, id, publicKey}, nil)
	if !ed25519.Verify(ed25519.PublicKey(publicKey), signed, signature) {
		return ErrReceiverAuthentication
	}
	c.receiverPairingID = string(id)
	c.receiverPublicKey = append([]byte(nil), publicKey...)
	return nil
}

func (c *AirPlayClient) authenticateReceiverVerify(plain, serverPublic, clientPublic []byte) error {
	if len(c.receiverPublicKey) != ed25519.PublicKeySize || c.receiverPairingID == "" {
		return ErrReceiverAuthentication
	}
	identity := tlv8Decode(plain)
	id, signature := identity[tlvIdentifier], identity[tlvSignature]
	if string(id) != c.receiverPairingID || len(signature) != ed25519.SignatureSize {
		return ErrReceiverAuthentication
	}
	signed := bytes.Join([][]byte{serverPublic, id, clientPublic}, nil)
	if !ed25519.Verify(ed25519.PublicKey(c.receiverPublicKey), signed, signature) {
		return ErrReceiverAuthentication
	}
	return nil
}
