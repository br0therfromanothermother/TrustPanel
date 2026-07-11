// Package v2raystats is a tiny gRPC client for sing-box's embedded v2ray
// StatsService (experimental.v2ray_api). The entry node's sing-box exposes
// per-user cumulative byte counters named
//
//	user>>>{username}>>>traffic>>>uplink
//	user>>>{username}>>>traffic>>>downlink
//
// over the gRPC service v2ray.core.app.stats.command.StatsService. We call
// QueryStats(pattern:"user>>>", reset:false) and parse the names back into
// per-user uplink/downlink totals.
//
// The two protobuf messages are hand-rolled (encode/decode the wire format) and
// pushed through gRPC with a raw codec, so we depend only on google.golang.org/
// grpc — not on all of sing-box/v2ray-core or a protoc toolchain.
package v2raystats

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// statsMethod is the fully-qualified gRPC method for QueryStats.
const statsMethod = "/v2ray.core.app.stats.command.StatsService/QueryStats"

// userPrefix matches every per-user counter; the suffix selects the direction.
const (
	sep            = ">>>"
	userPrefix     = "user" + sep
	uplinkSuffix   = sep + "traffic" + sep + "uplink"
	downlinkSuffix = sep + "traffic" + sep + "downlink"
)

// UserStat is one user's cumulative byte counters (since sing-box start).
type UserStat struct {
	Username string
	Uplink   int64 // client -> server bytes
	Downlink int64 // server -> client bytes
}

// Querier returns per-user cumulative traffic. It is the seam the agent depends
// on, so tests can fake it without a real sing-box.
type Querier interface {
	QueryUsers(ctx context.Context) ([]UserStat, error)
}

// Client is a gRPC Querier backed by a sing-box v2ray_api listener.
type Client struct {
	conn *grpc.ClientConn
}

// Dial creates a (lazy) gRPC client for the v2ray stats API at addr
// (e.g. "127.0.0.1:8088"). The connection is established on first use.
func Dial(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("v2raystats: dial %s: %w", addr, err)
	}
	return &Client{conn: conn}, nil
}

// Close releases the underlying connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// QueryUsers calls QueryStats(pattern:"user>>>", reset:false) and folds the flat
// stat list into per-user totals.
func (c *Client) QueryUsers(ctx context.Context) ([]UserStat, error) {
	stats, err := c.query(ctx, userPrefix, false)
	if err != nil {
		return nil, err
	}
	return foldUserStats(stats), nil
}

// stat is one (name, value) pair from QueryStatsResponse.
type stat struct {
	Name  string
	Value int64
}

func (c *Client) query(ctx context.Context, pattern string, reset bool) ([]stat, error) {
	req := marshalQueryStatsRequest(pattern, reset)
	var resp []byte
	if err := c.conn.Invoke(ctx, statsMethod, req, &resp, grpc.ForceCodec(rawCodec{})); err != nil {
		return nil, fmt.Errorf("v2raystats: QueryStats: %w", err)
	}
	return parseQueryStatsResponse(resp)
}

// foldUserStats turns the flat stat list into per-user totals, preserving
// first-seen order so output is deterministic.
func foldUserStats(stats []stat) []UserStat {
	idx := map[string]int{}
	var out []UserStat
	get := func(name string) *UserStat {
		if i, ok := idx[name]; ok {
			return &out[i]
		}
		idx[name] = len(out)
		out = append(out, UserStat{Username: name})
		return &out[len(out)-1]
	}
	for _, s := range stats {
		if !strings.HasPrefix(s.Name, userPrefix) {
			continue
		}
		rest := s.Name[len(userPrefix):]
		switch {
		case strings.HasSuffix(rest, uplinkSuffix):
			get(strings.TrimSuffix(rest, uplinkSuffix)).Uplink = s.Value
		case strings.HasSuffix(rest, downlinkSuffix):
			get(strings.TrimSuffix(rest, downlinkSuffix)).Downlink = s.Value
		}
	}
	return out
}

// ---- protobuf wire format (hand-rolled) ----
//
// QueryStatsRequest { string pattern = 1; bool reset = 2; }
// QueryStatsResponse { repeated Stat stat = 1; }
// Stat { string name = 1; int64 value = 2; }

func marshalQueryStatsRequest(pattern string, reset bool) []byte {
	var b []byte
	b = appendTag(b, 1, 2) // pattern (length-delimited)
	b = appendBytes(b, []byte(pattern))
	if reset {
		b = appendTag(b, 2, 0) // reset (varint)
		b = appendVarint(b, 1)
	}
	return b
}

func parseQueryStatsResponse(data []byte) ([]stat, error) {
	var out []stat
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, fmt.Errorf("v2raystats: bad tag varint")
		}
		data = data[n:]
		field, wire := tag>>3, tag&7
		if field == 1 && wire == 2 { // repeated Stat
			msg, rest, err := consumeBytes(data)
			if err != nil {
				return nil, err
			}
			data = rest
			s, err := parseStat(msg)
			if err != nil {
				return nil, err
			}
			out = append(out, s)
			continue
		}
		var err error
		if data, err = skipField(data, wire); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func parseStat(data []byte) (stat, error) {
	var s stat
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 {
			return s, fmt.Errorf("v2raystats: bad stat tag")
		}
		data = data[n:]
		field, wire := tag>>3, tag&7
		switch {
		case field == 1 && wire == 2: // name
			b, rest, err := consumeBytes(data)
			if err != nil {
				return s, err
			}
			s.Name, data = string(b), rest
		case field == 2 && wire == 0: // value
			v, n := binary.Uvarint(data)
			if n <= 0 {
				return s, fmt.Errorf("v2raystats: bad stat value")
			}
			s.Value, data = int64(v), data[n:]
		default:
			var err error
			if data, err = skipField(data, wire); err != nil {
				return s, err
			}
		}
	}
	return s, nil
}

// skipField advances past one field of the given wire type (for forward-compat
// with unknown fields).
func skipField(data []byte, wire uint64) ([]byte, error) {
	switch wire {
	case 0: // varint
		_, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, fmt.Errorf("v2raystats: bad varint")
		}
		return data[n:], nil
	case 1: // 64-bit
		if len(data) < 8 {
			return nil, fmt.Errorf("v2raystats: short 64-bit field")
		}
		return data[8:], nil
	case 2: // length-delimited
		_, rest, err := consumeBytes(data)
		return rest, err
	case 5: // 32-bit
		if len(data) < 4 {
			return nil, fmt.Errorf("v2raystats: short 32-bit field")
		}
		return data[4:], nil
	default:
		return nil, fmt.Errorf("v2raystats: unsupported wire type %d", wire)
	}
}

func consumeBytes(data []byte) (val, rest []byte, err error) {
	l, n := binary.Uvarint(data)
	if n <= 0 {
		return nil, nil, fmt.Errorf("v2raystats: bad length varint")
	}
	data = data[n:]
	if uint64(len(data)) < l {
		return nil, nil, fmt.Errorf("v2raystats: truncated length-delimited field")
	}
	return data[:l], data[l:], nil
}

func appendVarint(b []byte, v uint64) []byte {
	return binary.AppendUvarint(b, v)
}

func appendTag(b []byte, field, wire uint64) []byte {
	return appendVarint(b, field<<3|wire)
}

func appendBytes(b, data []byte) []byte {
	b = appendVarint(b, uint64(len(data)))
	return append(b, data...)
}

// rawCodec passes []byte payloads straight through gRPC, so we can ship our
// hand-rolled protobuf without registering a real proto codec.
type rawCodec struct{}

func (rawCodec) Marshal(v any) ([]byte, error) {
	b, ok := v.([]byte)
	if !ok {
		return nil, fmt.Errorf("v2raystats: rawCodec marshal: want []byte, got %T", v)
	}
	return b, nil
}

func (rawCodec) Unmarshal(data []byte, v any) error {
	p, ok := v.(*[]byte)
	if !ok {
		return fmt.Errorf("v2raystats: rawCodec unmarshal: want *[]byte, got %T", v)
	}
	*p = append((*p)[:0], data...)
	return nil
}

func (rawCodec) Name() string { return "trustpanel-raw" }
