package v2raystats

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc"
)

// marshalQueryStatsResponse builds a QueryStatsResponse from stats, exercising
// the same wire helpers the client decodes.
func marshalQueryStatsResponse(stats []stat) []byte {
	var b []byte
	for _, s := range stats {
		var msg []byte
		msg = appendTag(msg, 1, 2) // name
		msg = appendBytes(msg, []byte(s.Name))
		msg = appendTag(msg, 2, 0) // value
		msg = appendVarint(msg, uint64(s.Value))
		b = appendTag(b, 1, 2) // repeated Stat
		b = appendBytes(b, msg)
	}
	return b
}

func TestFoldUserStats(t *testing.T) {
	sample := []stat{
		{Name: "user>>>alice>>>traffic>>>uplink", Value: 100},
		{Name: "user>>>alice>>>traffic>>>downlink", Value: 900},
		{Name: "user>>>bob>>>traffic>>>uplink", Value: 5},
		// bob has no downlink counter yet -> should default to 0.
		{Name: "inbound>>>socks-in>>>traffic>>>uplink", Value: 7}, // non-user, ignored
	}
	got := foldUserStats(sample)
	want := []UserStat{
		{Username: "alice", Uplink: 100, Downlink: 900},
		{Username: "bob", Uplink: 5, Downlink: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("foldUserStats:\n got %+v\nwant %+v", got, want)
	}
}

func TestWireRoundTrip(t *testing.T) {
	// A request marshals and a response unmarshals through the hand-rolled codec.
	req := marshalQueryStatsRequest("user>>>", false)
	if len(req) == 0 {
		t.Fatal("empty request")
	}
	in := []stat{
		{Name: "user>>>carol>>>traffic>>>uplink", Value: 1 << 40}, // large value -> multi-byte varint
		{Name: "user>>>carol>>>traffic>>>downlink", Value: 42},
	}
	parsed, err := parseQueryStatsResponse(marshalQueryStatsResponse(in))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed, in) {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", parsed, in)
	}
}

// fakeStatsServer answers QueryStats with a canned response over a raw codec,
// standing in for sing-box's v2ray_api so the gRPC client can be exercised
// without a real sing-box.
func fakeStatsServer(t *testing.T, resp []stat) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.ForceServerCodec(rawCodec{}))
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "v2ray.core.app.stats.command.StatsService",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "QueryStats",
			Handler: func(_ any, _ context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
				var in []byte
				if err := dec(&in); err != nil {
					return nil, err
				}
				return marshalQueryStatsResponse(resp), nil
			},
		}},
	}, struct{}{})
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func TestClientQueryUsers(t *testing.T) {
	addr := fakeStatsServer(t, []stat{
		{Name: "user>>>alice>>>traffic>>>uplink", Value: 11},
		{Name: "user>>>alice>>>traffic>>>downlink", Value: 22},
	})
	c, err := Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := c.QueryUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []UserStat{{Username: "alice", Uplink: 11, Downlink: 22}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("QueryUsers:\n got %+v\nwant %+v", got, want)
	}
}
