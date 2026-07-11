package clientcfg

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// DeepLink is the decoded content of a tt://? deep link (DEEP_LINK.md). It is
// the inverse of buildDeepLinkPayload: it lets the operator import an existing
// TrustTunnel client config (e.g. pasted into the bot) back into the panel.
type DeepLink struct {
	Hostname  string
	Username  string
	Password  string
	Name      string
	Addresses []string
}

// ParseDeepLink decodes a tt://? URI — or a bare base64url payload, or a landing
// link carrying a #tt= / ?tt= fragment — into its fields. Unknown TLV tags are
// ignored, per the spec. It errors if the payload is malformed or is missing the
// fields a usable config must carry (hostname, username, password).
func ParseDeepLink(s string) (DeepLink, error) {
	payload := extractPayload(s)
	if payload == "" {
		return DeepLink{}, fmt.Errorf("empty deep link")
	}
	raw, err := decodeB64(payload)
	if err != nil {
		return DeepLink{}, fmt.Errorf("not a valid deep link (bad base64)")
	}

	var dl DeepLink
	i := 0
	for i < len(raw) {
		tag, n, err := getVarint(raw, i)
		if err != nil {
			return DeepLink{}, err
		}
		i = n
		length, n2, err := getVarint(raw, i)
		if err != nil {
			return DeepLink{}, err
		}
		i = n2
		if i+int(length) > len(raw) {
			return DeepLink{}, fmt.Errorf("truncated deep link")
		}
		val := raw[i : i+int(length)]
		i += int(length)
		switch tag {
		case 0x01:
			dl.Hostname = string(val)
		case 0x02:
			dl.Addresses = append(dl.Addresses, string(val))
		case 0x05:
			dl.Username = string(val)
		case 0x06:
			dl.Password = string(val)
		case 0x0C:
			dl.Name = string(val)
		}
	}
	if dl.Hostname == "" || dl.Username == "" || dl.Password == "" {
		return DeepLink{}, fmt.Errorf("deep link is missing hostname/username/password")
	}
	return dl, nil
}

// extractPayload strips a tt:// scheme or a #tt=/?tt= fragment to leave the bare
// base64url payload.
func extractPayload(s string) string {
	s = strings.TrimSpace(s)
	for _, sep := range []string{"#tt=", "?tt=", "&tt="} {
		if i := strings.Index(s, sep); i >= 0 {
			s = s[i+len(sep):]
			if j := strings.IndexAny(s, "&\t\n\r "); j >= 0 {
				s = s[:j]
			}
			return strings.TrimSpace(s)
		}
	}
	if rest, ok := strings.CutPrefix(s, "tt://?"); ok {
		return rest
	}
	if rest, ok := strings.CutPrefix(s, "tt://"); ok {
		return rest
	}
	return s
}

// decodeB64 accepts either base64url or std base64, with or without padding.
func decodeB64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{
		base64.RawURLEncoding, base64.URLEncoding,
		base64.RawStdEncoding, base64.StdEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("invalid base64")
}

// getVarint reads a QUIC/TLS variable-length integer (RFC 9000 §16) at offset i,
// returning the value and the index just past it. It is the inverse of putVarint.
func getVarint(b []byte, i int) (uint64, int, error) {
	if i >= len(b) {
		return 0, 0, fmt.Errorf("truncated deep link")
	}
	f := b[i]
	switch f >> 6 {
	case 0:
		return uint64(f & 0x3f), i + 1, nil
	case 1:
		if i+1 >= len(b) {
			return 0, 0, fmt.Errorf("truncated deep link")
		}
		return uint64(f&0x3f)<<8 | uint64(b[i+1]), i + 2, nil
	case 2:
		if i+3 >= len(b) {
			return 0, 0, fmt.Errorf("truncated deep link")
		}
		return uint64(f&0x3f)<<24 | uint64(b[i+1])<<16 | uint64(b[i+2])<<8 | uint64(b[i+3]), i + 4, nil
	default:
		if i+7 >= len(b) {
			return 0, 0, fmt.Errorf("truncated deep link")
		}
		v := uint64(f & 0x3f)
		for k := 1; k < 8; k++ {
			v = v<<8 | uint64(b[i+k])
		}
		return v, i + 8, nil
	}
}
