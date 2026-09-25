package provision

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"trustpanel/internal/core/model"
)

// NewRealityDialIn generates a fresh set of VLESS+Reality keys and wraps them in
// a dial-in for an exit's :443 listener borrowing targetSNI. It is the single
// constructor for an exit dial-in, shared by the panel install/migrate paths and
// the break-glass CLI so the proto/port/field layout can't drift between them.
func NewRealityDialIn(targetSNI string) (*model.DialIn, error) {
	uuid, pub, priv, sid, err := GenerateReality()
	if err != nil {
		return nil, err
	}
	return &model.DialIn{
		Proto: model.DialInProtoVLESSReality, Port: 443, UUID: uuid,
		TargetSNI: targetSNI, PublicKey: pub, PrivKey: priv, ShortID: sid,
	}, nil
}

// GenerateReality generates VLESS+Reality material for an exit node: a v4 uuid,
// an X25519 keypair (base64 raw-url, the Reality encoding), and an 8-hex short
// id. Shared by the panel install endpoint and the break-glass CLI so both can
// register a valid exit node.
func GenerateReality() (uuid, pub, priv, shortID string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", "", "", err
	}
	priv = base64.RawURLEncoding.EncodeToString(k.Bytes())
	pub = base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes())
	if uuid, err = genUUID(); err != nil {
		return "", "", "", "", err
	}
	sid := make([]byte, 4)
	if _, err = rand.Read(sid); err != nil {
		return "", "", "", "", err
	}
	return uuid, pub, priv, hex.EncodeToString(sid), nil
}

func genUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
