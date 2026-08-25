package fins

import (
	"net"
	"testing"
)

func TestResolveSrcNode(t *testing.T) {
	cases := []struct {
		name       string
		configured byte
		ip         net.IP
		want       byte
	}{
		{"unset derives from local ip", 0, net.ParseIP("10.62.100.112"), 112},
		{"unset with no local ip falls back to default", 0, nil, defaultSrcNode},
		{"unset with ip octet 0 falls back", 0, net.ParseIP("10.62.100.0"), defaultSrcNode},
		{"unset with ip octet 255 falls back", 0, net.ParseIP("10.62.100.255"), defaultSrcNode},
		{"unset with ipv6 falls back", 0, net.ParseIP("fe80::1"), defaultSrcNode},
		{"explicit valid wins", 5, net.ParseIP("10.62.100.112"), 5},
		{"explicit 255 treated as unset", 255, net.ParseIP("10.62.100.112"), 112},
		{"explicit valid with no ip", 16, nil, 16},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveSrcNode(c.configured, c.ip); got != c.want {
				t.Errorf("resolveSrcNode(%d, %v) = %d, want %d", c.configured, c.ip, got, c.want)
			}
		})
	}
}
