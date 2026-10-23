package airplay

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

func authenticatedTestIdentity(private ed25519.PrivateKey, shared, sessionKey []byte, corrupt bool) []byte {
	id := []byte("fixture-receiver-identity")
	public := private.Public().(ed25519.PublicKey)
	signKey := hkdfSHA512(shared, []byte("Pair-Setup-Accessory-Sign-Salt"), []byte("Pair-Setup-Accessory-Sign-Info"), 32)
	signature := ed25519.Sign(private, bytes.Join([][]byte{signKey, id, public}, nil))
	if corrupt {
		signature[0] ^= 1
	}
	plain := tlv8EncodeOrdered([]tlv8Item{{Tag: tlvIdentifier, Value: id}, {Tag: tlvPublicKey, Value: public}, {Tag: tlvSignature, Value: signature}})
	aead, _ := chacha20poly1305.New(sessionKey)
	return aead.Seal(nil, receiverPairingNonce("PS-Msg06"), plain, nil)
}

func TestReceiverSetupAuthenticatesSignatureAndPinsIdentity(t *testing.T) {
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x43}, 32))
	shared, sessionKey := bytes.Repeat([]byte{0x24}, 64), bytes.Repeat([]byte{0x17}, 32)
	client := NewAirPlayClient("", 0)
	client.RequireReceiverAuthentication()
	if err := client.authenticateReceiverSetup(authenticatedTestIdentity(private, shared, sessionKey, true), sessionKey, shared); !errors.Is(err, ErrReceiverAuthentication) {
		t.Fatalf("forged setup proof accepted: %v", err)
	}
	if _, key := client.ReceiverPairingIdentity(); len(key) != 0 {
		t.Fatal("failed setup installed a receiver identity")
	}
	if err := client.authenticateReceiverSetup(authenticatedTestIdentity(private, shared, sessionKey, false), sessionKey, shared); err != nil {
		t.Fatal(err)
	}
	id, key := client.ReceiverPairingIdentity()
	if id != "fixture-receiver-identity" || !bytes.Equal(key, private.Public().(ed25519.PublicKey)) {
		t.Fatal("receiver identity was not pinned")
	}
	for _, body := range [][]byte{nil, {1, 2, 3}, make([]byte, 16)} {
		if err := client.authenticateReceiverSetup(body, sessionKey, shared); !errors.Is(err, ErrReceiverAuthentication) {
			t.Fatalf("unauthenticated setup accepted: %v", err)
		}
	}
}

func TestReceiverVerifyRequiresPinnedSigningIdentity(t *testing.T) {
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x43}, 32))
	serverPublic, clientPublic := bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32)
	client := NewAirPlayClient("", 0)
	client.receiverPairingID = "fixture-receiver-identity"
	client.receiverPublicKey = private.Public().(ed25519.PublicKey)
	id := []byte(client.receiverPairingID)
	signature := ed25519.Sign(private, bytes.Join([][]byte{serverPublic, id, clientPublic}, nil))
	identity := tlv8EncodeOrdered([]tlv8Item{{Tag: tlvIdentifier, Value: id}, {Tag: tlvSignature, Value: signature}})
	if err := client.authenticateReceiverVerify(identity, serverPublic, clientPublic); err != nil {
		t.Fatal(err)
	}
	signature[0] ^= 1
	forged := tlv8EncodeOrdered([]tlv8Item{{Tag: tlvIdentifier, Value: id}, {Tag: tlvSignature, Value: signature}})
	if err := client.authenticateReceiverVerify(forged, serverPublic, clientPublic); !errors.Is(err, ErrReceiverAuthentication) {
		t.Fatalf("forged pair-verify signature accepted: %v", err)
	}
	client.receiverPairingID = "different-receiver"
	if err := client.authenticateReceiverVerify(identity, serverPublic, clientPublic); !errors.Is(err, ErrReceiverAuthentication) {
		t.Fatalf("changed receiver identity accepted: %v", err)
	}
}

func TestStrictPairingRejectsForgedReceiverProofOverControlConnection(t *testing.T) {
	for _, phase := range []string{"setup", "verify"} {
		t.Run(phase, func(t *testing.T) {
			private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x43}, 32))
			state, err := newReceiverPairingState(receiverPairingConfig{identifier: "fixture-receiver-identity", privateKey: private, pin: "4186"})
			if err != nil {
				t.Fatal(err)
			}
			connection, receiver := net.Pipe()
			defer connection.Close()
			defer receiver.Close()
			client := NewAirPlayClient("", 0)
			client.conn = connection
			client.RequireReceiverAuthentication()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
			defer stop()
			done := make(chan struct{})
			go func() {
				defer close(done)
				reader := bufio.NewReader(receiver)
				for {
					request, err := readRTSPTestRequest(reader)
					if err != nil {
						return
					}
					var response, shared []byte
					if state.setup != nil {
						shared = append([]byte(nil), state.setup.sharedKey...)
					}
					if request.uri == "/pair-setup" {
						response, err = state.pairSetup(request.body)
					} else {
						response, err = state.pairVerify(request.body)
					}
					if err != nil {
						return
					}
					message := tlv8Decode(response)
					if phase == "setup" && len(shared) != 0 && bytes.Equal(message[tlvState], []byte{6}) {
						key := hkdfSHA512(shared, []byte("Pair-Setup-Encrypt-Salt"), []byte("Pair-Setup-Encrypt-Info"), 32)
						message[tlvEncryptedData] = authenticatedTestIdentity(private, shared, key, true)
						response = tlv8EncodeOrdered([]tlv8Item{{Tag: tlvState, Value: []byte{6}}, {Tag: tlvEncryptedData, Value: message[tlvEncryptedData]}})
					}
					if phase == "verify" && request.uri == "/pair-verify" && bytes.Equal(message[tlvState], []byte{2}) {
						aead, _ := chacha20poly1305.New(state.verify.verifyKey)
						plain, _ := aead.Open(nil, receiverPairingNonce("PV-Msg02"), message[tlvEncryptedData], nil)
						identity := tlv8Decode(plain)
						identity[tlvSignature][0] ^= 1
						plain = tlv8EncodeOrdered([]tlv8Item{{Tag: tlvIdentifier, Value: identity[tlvIdentifier]}, {Tag: tlvSignature, Value: identity[tlvSignature]}})
						sealed := aead.Seal(nil, receiverPairingNonce("PV-Msg02"), plain, nil)
						response = tlv8EncodeOrdered([]tlv8Item{{Tag: tlvState, Value: []byte{2}}, {Tag: tlvPublicKey, Value: message[tlvPublicKey]}, {Tag: tlvEncryptedData, Value: sealed}})
					}
					if writeRTSPTestResponse(receiver, 200, nil, response) != nil {
						return
					}
				}
			}()
			if err := client.Pair(ctx, "4186"); !errors.Is(err, ErrReceiverAuthentication) {
				t.Fatalf("forged receiver %s proof error = %v", phase, err)
			}
			_ = connection.Close()
			<-done
		})
	}
}
